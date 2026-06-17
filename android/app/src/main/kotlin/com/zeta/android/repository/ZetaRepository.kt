package com.zeta.android.repository

import android.os.Build
import android.util.Log
import com.zeta.android.data.NodeState
import com.zeta.android.data.StateManager
import com.zeta.android.data.WireGuardKeyPair
import com.zeta.android.grpc.CoordinatorClient
import com.zeta.android.net.MeshDnsResolver
import com.zeta.android.net.StunClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import zeta.v1.EndpointUpdate
import zeta.v1.NetworkMap
import zeta.v1.PingUpdate
import zeta.v1.RegisterRequest
import zeta.v1.SyncResponse
import zeta.v1.SyncUpdate

class ZetaRepository(
    private val stateManager: StateManager,
    private val meshDns: MeshDnsResolver,
) {
    private val _nodeStateFlow = MutableStateFlow<NodeState?>(stateManager.loadNodeState())
    val nodeStateFlow: StateFlow<NodeState?> = _nodeStateFlow.asStateFlow()

    private val _networkMapFlow = MutableStateFlow<NetworkMap?>(null)
    val networkMapFlow: StateFlow<NetworkMap?> = _networkMapFlow.asStateFlow()

    val nodeState: NodeState? get() = _nodeStateFlow.value

    suspend fun enroll(
        coordinatorUrl: String,
        preauthKey: String,
    ): NodeState =
        withContext(Dispatchers.IO) {
            val kp = WireGuardKeyPair.generate()
            val hostname = "android-${Build.MODEL.lowercase().replace(" ", "-")}"

            val (host, port) = CoordinatorClient.parseUrl(coordinatorUrl)
            val client = CoordinatorClient.create(host, port)

            try {
                val req =
                    RegisterRequest
                        .newBuilder()
                        .setWgPublicKey(kp.publicKey)
                        .setHostname(hostname)
                        .setOs("android")
                        .setAgentVersion("1.0.0")
                        .setPreauthKey(preauthKey)
                        .build()

                val resp = client.register(req)
                if (resp.hasPending()) {
                    error("Browser auth not supported; use a preauth key")
                }
                val cfg = resp.config
                NodeState(
                    nodeId = cfg.nodeId,
                    meshIp = cfg.meshIp,
                    domain = cfg.domain,
                    wgPrivateKey = kp.privateKey,
                    wgPublicKey = kp.publicKey,
                    certPem = cfg.certPem.toStringUtf8(),
                    keyPem = cfg.keyPem.toStringUtf8(),
                    caPem = cfg.caPem.toStringUtf8(),
                    coordinatorUrl = coordinatorUrl,
                ).also { state ->
                    stateManager.saveNodeState(state)
                    _nodeStateFlow.value = state
                }
            } finally {
                client.shutdown()
            }
        }

    fun forgetDevice() {
        stateManager.clearNodeState()
        _nodeStateFlow.value = null
        _networkMapFlow.value = null
    }

    // discoverEndpoint: caller supplies a protected socket discoverer (VpnService.protect()).
    // Falls back to unprotected StunClient if not provided.
    // preDiscoveredEndpoint: endpoint found before the VPN started (correct WG port).
    suspend fun startSyncLoop(
        discoverEndpoint: suspend () -> String? = { StunClient.discoverEndpoint() },
        preDiscoveredEndpoint: String? = null,
        onNetworkMap: (NetworkMap) -> Unit,
    ) {
        val state = _nodeStateFlow.value ?: return
        val (host, port) = CoordinatorClient.parseUrl(state.coordinatorUrl)

        var backoff = 1_000L
        val maxBackoff = 60_000L
        var firstConnect = true

        while (true) {
            val client = CoordinatorClient.create(host, port)
            try {
                val (sendCh, responseFlow) = client.openSyncStream(state.nodeId)
                backoff = 1_000L

                // On the very first connection, immediately send the pre-VPN STUN endpoint
                // (correct WireGuard port) before entering the streaming coroutines.
                // On reconnects, run STUN fresh (with protect()) as the first action.
                if (firstConnect && preDiscoveredEndpoint != null) {
                    firstConnect = false
                    sendEndpointUpdate(sendCh, state.nodeId, preDiscoveredEndpoint)
                } else {
                    firstConnect = false
                    sendStunUpdate(sendCh, state.nodeId, discoverEndpoint)
                }

                kotlinx.coroutines.coroutineScope {
                    launch {
                        while (isActive) {
                            delay(30_000L)
                            sendStunUpdate(sendCh, state.nodeId, discoverEndpoint)
                        }
                    }
                    launch {
                        while (isActive) {
                            delay(30_000L)
                            sendCh.trySend(
                                SyncUpdate
                                    .newBuilder()
                                    .setPing(PingUpdate.newBuilder().setNodeId(state.nodeId).build())
                                    .build(),
                            )
                        }
                    }
                    launch {
                        responseFlow.collect { response ->
                            when (response.payloadCase) {
                                SyncResponse.PayloadCase.NETWORK_MAP -> {
                                    val nm = response.networkMap
                                    _networkMapFlow.value = nm
                                    val domain =
                                        nm.dns
                                            ?.meshDomain
                                            ?.takeIf { it.isNotBlank() } ?: "mesh"
                                    meshDns.updateFromPeers(nm.peersList, domain)
                                    onNetworkMap(nm)
                                }

                                else -> {}
                            }
                        }
                    }
                }
            } catch (e: CancellationException) {
                client.shutdown()
                throw e
            } catch (e: Exception) {
                Log.w(TAG, "Sync stream error, retrying in ${backoff}ms", e)
            } finally {
                client.shutdown()
            }

            delay(backoff)
            backoff = minOf(backoff * 2, maxBackoff)
        }
    }

    private fun sendEndpointUpdate(
        sendCh: kotlinx.coroutines.channels.SendChannel<SyncUpdate>,
        nodeId: String,
        endpoint: String,
    ) {
        sendCh.trySend(
            SyncUpdate.newBuilder()
                .setEndpoint(
                    EndpointUpdate.newBuilder()
                        .setNodeId(nodeId)
                        .setEndpoint(endpoint)
                        .build(),
                )
                .build(),
        )
        Log.d(TAG, "Sent endpoint update: $endpoint")
    }

    private suspend fun sendStunUpdate(
        sendCh: kotlinx.coroutines.channels.SendChannel<SyncUpdate>,
        nodeId: String,
        discoverEndpoint: suspend () -> String?,
    ) {
        withContext(Dispatchers.IO) {
            val endpoint = discoverEndpoint()
            if (endpoint != null) {
                sendCh.trySend(
                    SyncUpdate
                        .newBuilder()
                        .setEndpoint(
                            EndpointUpdate
                                .newBuilder()
                                .setNodeId(nodeId)
                                .setEndpoint(endpoint)
                                .build(),
                        ).build(),
                )
            }
        }
    }

    companion object {
        private const val TAG = "ZetaRepository"
    }
}
