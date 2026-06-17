package com.zeta.android.vpn

import android.app.Notification
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.util.Log
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import com.wireguard.config.InetNetwork
import com.wireguard.config.Interface
import com.wireguard.crypto.Key
import zeta.v1.NetworkMap
import com.zeta.android.MainActivity
import com.zeta.android.ZetaApplication
import com.zeta.android.net.StunClient
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class ZetaVpnService : GoBackend.VpnService() {

    inner class LocalBinder : android.os.Binder() {
        fun getService(): ZetaVpnService = this@ZetaVpnService
    }

    private val binder = LocalBinder()
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var syncJob: Job? = null

    private val _vpnState = MutableStateFlow(VpnState.DISCONNECTED)
    val vpnState: StateFlow<VpnState> = _vpnState.asStateFlow()

    @Volatile private var cachedEndpoint: String? = null

    private val zetaTunnel = object : Tunnel {
        override fun getName() = "zeta"
        override fun onStateChange(newState: Tunnel.State) {
            _vpnState.value = when (newState) {
                Tunnel.State.UP -> VpnState.CONNECTED
                Tunnel.State.DOWN -> VpnState.DISCONNECTED
                Tunnel.State.TOGGLE -> VpnState.CONNECTING
            }
        }
    }

    override fun onBind(intent: android.content.Intent?): android.os.IBinder {
        // GoBackend.VpnService.onBind handles VPN binding; our LocalBinder is for Activity use.
        return if (intent?.action == SERVICE_INTERFACE) super.onBind(intent)!!
        else binder
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startVpn()
            ACTION_STOP -> stopVpn()
        }
        return START_STICKY
    }

    private fun startVpn() {
        val app = application as ZetaApplication
        val state = app.repository.nodeState ?: run { stopSelf(); return }

        _vpnState.value = VpnState.CONNECTING
        startForeground(NOTIF_ID, buildNotification("Connecting…"), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)

        // All network I/O and WireGuard setup runs on the IO thread — doing any of this
        // on the main thread (onStartCommand) throws NetworkOnMainThreadException.
        syncJob = serviceScope.launch {
            // Discover STUN endpoint before WireGuard starts (port 51820 is still free).
            val preVpnEndpoint: String? = try {
                val ipv4Any = InetAddress.getByName("0.0.0.0") as Inet4Address
                val socket = DatagramSocket(null).apply {
                    bind(InetSocketAddress(ipv4Any, WG_LISTEN_PORT))
                }
                val ep = socket.use { StunClient.discoverWithSocket(it) }
                Log.i(TAG, "Pre-VPN STUN → $ep")
                ep
            } catch (e: Exception) {
                Log.w(TAG, "Pre-VPN STUN failed: ${e.message}")
                null
            }
            cachedEndpoint = preVpnEndpoint

            val initialConfig = buildWgConfig(state.wgPrivateKey, state.meshIp, emptyList())
            try {
                app.wgBackend.setState(zetaTunnel, Tunnel.State.UP, initialConfig)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to start WireGuard tunnel", e)
                _vpnState.value = VpnState.ERROR
                stopSelf()
                return@launch
            }

            updateNotification("Connected — ${state.meshIp}")

            app.repository.startSyncLoop(
                discoverEndpoint = { discoverProtectedEndpoint() },
                onNetworkMap = { nm -> applyNetworkMap(nm, state.meshIp) },
                preDiscoveredEndpoint = preVpnEndpoint,
            )
        }
    }

    // Called from the sync coroutine — protect() exempts the socket from the VPN TUN
    // so the packet goes out via the real network interface, not tun0.
    // Binds to an ephemeral port (WG_LISTEN_PORT is already owned by WireGuard), then
    // substitutes WG_LISTEN_PORT into the result so the reported endpoint matches the
    // port that WireGuard is actually listening on. Falls back to the last known good
    // endpoint so the coordinator always has something to work with.
    private fun discoverProtectedEndpoint(): String? {
        val ep = try {
            val ipv4Any = InetAddress.getByName("0.0.0.0") as Inet4Address
            val socket = DatagramSocket(null).apply {
                bind(InetSocketAddress(ipv4Any, 0))
            }
            protect(socket)
            val raw = socket.use { StunClient.discoverWithSocket(it) }
            // STUN reflects the ephemeral port; replace it with WG_LISTEN_PORT so peers
            // can reach WireGuard's actual socket (symmetric-NAT issue otherwise).
            raw?.let { "${it.substringBeforeLast(":")}:$WG_LISTEN_PORT" }
        } catch (e: Exception) {
            Log.w(TAG, "Protected STUN failed: ${e.message}")
            null
        }
        return if (ep != null) {
            cachedEndpoint = ep
            Log.i(TAG, "Protected STUN → $ep")
            ep
        } else {
            Log.w(TAG, "Protected STUN returned null, using cached endpoint: $cachedEndpoint")
            cachedEndpoint
        }
    }

    private fun stopVpn() {
        syncJob?.cancel()
        syncJob = null
        try {
            (application as ZetaApplication).wgBackend.setState(zetaTunnel, Tunnel.State.DOWN, null)
        } catch (_: Exception) {}
        _vpnState.value = VpnState.DISCONNECTED
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun applyNetworkMap(nm: NetworkMap, selfMeshIp: String) {
        val state = (application as ZetaApplication).repository.nodeState ?: return
        val protoPeers = nm.peersList.filter { it.meshIp != selfMeshIp }
        val config = buildWgConfig(state.wgPrivateKey, state.meshIp, protoPeers)
        try {
            (application as ZetaApplication).wgBackend.setState(zetaTunnel, Tunnel.State.UP, config)
        } catch (e: Exception) {
            Log.w(TAG, "Failed to apply peer update", e)
        }
    }

    private fun buildWgConfig(privateKeyB64: String, meshIp: String, protoPeers: List<zeta.v1.Peer>): Config {
        val iface = Interface.Builder()
            .parsePrivateKey(privateKeyB64)
            .addAddress(InetNetwork.parse("$meshIp/10"))
            .setListenPort(WG_LISTEN_PORT)
            .build()

        val wgPeers = protoPeers.mapNotNull { peer ->
            if (peer.wgPublicKey.isBlank() || peer.meshIp.isBlank()) return@mapNotNull null
            runCatching {
                val pb = com.wireguard.config.Peer.Builder()
                    .setPublicKey(Key.fromBase64(peer.wgPublicKey))
                    .addAllowedIp(InetNetwork.parse("${peer.meshIp}/32"))
                    .setPersistentKeepalive(25)
                if (peer.endpoint.isNotBlank()) {
                    runCatching { pb.parseEndpoint(peer.endpoint) }
                }
                pb.build()
            }.getOrNull()
        }

        return Config.Builder()
            .setInterface(iface)
            .addPeers(wgPeers)
            .build()
    }

    private fun buildNotification(text: String): Notification {
        val pi = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("Zeta")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentIntent(pi)
            .setOngoing(true)
            .build()
    }

    private fun updateNotification(text: String) {
        getSystemService(android.app.NotificationManager::class.java)
            .notify(NOTIF_ID, buildNotification(text))
    }

    override fun onDestroy() {
        serviceScope.cancel()
        runCatching { (application as ZetaApplication).wgBackend.setState(zetaTunnel, Tunnel.State.DOWN, null) }
        super.onDestroy()
    }

    enum class VpnState { DISCONNECTED, CONNECTING, CONNECTED, ERROR }

    companion object {
        const val ACTION_START = "com.zeta.android.VPN_START"
        const val ACTION_STOP = "com.zeta.android.VPN_STOP"
        const val NOTIF_ID = 1001
        const val CHANNEL_ID = "zeta_vpn"
        const val WG_LISTEN_PORT = 51820
        private const val TAG = "ZetaVpnService"
    }
}
