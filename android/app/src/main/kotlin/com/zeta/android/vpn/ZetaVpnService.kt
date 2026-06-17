package com.zeta.android.vpn

import android.app.Notification
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Base64
import android.util.Log
import com.zeta.android.MainActivity
import com.zeta.android.ZetaApplication
import com.zeta.android.net.StunClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject
import vpnlib.Vpnlib
import zeta.v1.NetworkMap
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress

class ZetaVpnService : VpnService() {

    inner class LocalBinder : android.os.Binder() {
        fun getService(): ZetaVpnService = this@ZetaVpnService
    }

    private val binder = LocalBinder()
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var syncJob: Job? = null

    private val _vpnState = MutableStateFlow(VpnState.DISCONNECTED)
    val vpnState: StateFlow<VpnState> = _vpnState.asStateFlow()

    private var vpnHandle = -1L // gomobile maps Go int → Java long
    private var tunPfd: ParcelFileDescriptor? = null

    @Volatile private var cachedEndpoint: String? = null

    private fun setVpnState(state: VpnState) {
        _vpnState.value = state
        (application as ZetaApplication).vpnState.value = state
    }

    override fun onBind(intent: android.content.Intent?): android.os.IBinder {
        return if (intent?.action == SERVICE_INTERFACE) super.onBind(intent) ?: binder
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

        setVpnState(VpnState.CONNECTING)
        startForeground(NOTIF_ID, buildNotification("Connecting…"), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)

        syncJob = serviceScope.launch {
            // Capture upstream DNS before establishing the VPN (100.64.0.1 not yet active).
            val upstream = getSystemUpstreamDns() ?: "1.1.1.1:53"

            // Pre-VPN STUN: discover our WireGuard endpoint before the TUN intercepts traffic.
            val preVpnEndpoint: String? = try {
                val socket = DatagramSocket(null).apply {
                    bind(InetSocketAddress(InetAddress.getByName("0.0.0.0") as Inet4Address, WG_LISTEN_PORT))
                }
                val ep = socket.use { StunClient.discoverWithSocket(it) }
                Log.i(TAG, "Pre-VPN STUN → $ep")
                ep
            } catch (e: Exception) {
                Log.w(TAG, "Pre-VPN STUN failed: ${e.message}")
                null
            }
            cachedEndpoint = preVpnEndpoint

            val pfd = Builder()
                .setSession("zeta")
                .addAddress(state.meshIp, 10)
                .addRoute("100.64.0.0", 10)
                .addDnsServer("100.64.0.1")
                .addSearchDomain(state.domain)
                .setMtu(1280)
                .establish() ?: run { stopSelf(); return@launch }
            tunPfd = pfd

            // detachFd() transfers ownership to our Go library — pfd must not be closed.
            val fd = pfd.detachFd()
            vpnHandle = Vpnlib.start(fd.toLong(), 1280L, buildWgConf(state.wgPrivateKey, emptyList()), upstream)
            if (vpnHandle < 0L) {
                Log.e(TAG, "vpnlib start failed: ${Vpnlib.error()}")
                setVpnState(VpnState.ERROR)
                stopSelf()
                return@launch
            }
            setVpnState(VpnState.CONNECTED)
            updateNotification("Connected — ${state.meshIp}")

            app.repository.startSyncLoop(
                discoverEndpoint = { discoverProtectedEndpoint() },
                onNetworkMap = { nm -> applyNetworkMap(nm, state.meshIp) },
                preDiscoveredEndpoint = preVpnEndpoint,
            )
        }
    }

    // Discover our public endpoint after VPN is up. protect() exempts the socket from the TUN.
    private fun discoverProtectedEndpoint(): String? {
        val ep = try {
            val socket = DatagramSocket(null).apply {
                bind(InetSocketAddress(InetAddress.getByName("0.0.0.0") as Inet4Address, 0))
            }
            protect(socket)
            val raw = socket.use { StunClient.discoverWithSocket(it) }
            // Replace ephemeral port with WG_LISTEN_PORT so peers reach WireGuard's actual socket.
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
            Log.w(TAG, "Protected STUN null, using cached: $cachedEndpoint")
            cachedEndpoint
        }
    }

    private fun applyNetworkMap(nm: NetworkMap, selfMeshIp: String) {
        val state = (application as ZetaApplication).repository.nodeState ?: return
        val peers = nm.peersList.filter { it.meshIp != selfMeshIp }
        Vpnlib.setConfig(vpnHandle, buildWgConf(state.wgPrivateKey, peers))
        Vpnlib.setDNSRecords(vpnHandle, buildDnsJson(nm))
    }

    private fun buildWgConf(privateKeyB64: String, peers: List<zeta.v1.Peer>): String = buildString {
        append("private_key=${base64ToHex(privateKeyB64)}\n")
        append("listen_port=$WG_LISTEN_PORT\n")
        append("replace_peers=true\n")
        for (peer in peers) {
            if (peer.wgPublicKey.isBlank() || peer.meshIp.isBlank()) continue
            append("public_key=${base64ToHex(peer.wgPublicKey)}\n")
            append("allowed_ip=${peer.meshIp}/32\n")
            append("persistent_keepalive_interval=25\n")
            if (peer.endpoint.isNotBlank()) append("endpoint=${peer.endpoint}\n")
        }
    }

    private fun buildDnsJson(nm: NetworkMap): String {
        val meshDomain = nm.dns?.meshDomain?.takeIf { it.isNotBlank() } ?: "mesh"
        val map = mutableMapOf<String, String>()
        for (peer in nm.peersList) {
            if (peer.hostname.isBlank() || peer.meshIp.isBlank()) continue
            map["${peer.hostname}.$meshDomain"] = peer.meshIp
            for (svc in peer.servicesList) {
                if (svc.name.isNotBlank()) map["${svc.name}.${peer.hostname}.$meshDomain"] = peer.meshIp
            }
        }
        return JSONObject(map as Map<*, *>).toString()
    }

    private fun getSystemUpstreamDns(): String? {
        val cm = getSystemService(ConnectivityManager::class.java)
        val props = cm.getLinkProperties(cm.activeNetwork) ?: return null
        return props.dnsServers.firstOrNull()?.hostAddress?.let { "$it:53" }
    }

    private fun base64ToHex(b64: String): String =
        Base64.decode(b64, Base64.DEFAULT).joinToString("") { "%02x".format(it) }

    private fun stopVpn() {
        syncJob?.cancel()
        syncJob = null
        if (vpnHandle >= 0L) {
            Vpnlib.stop(vpnHandle)
            vpnHandle = -1L
        }
        tunPfd?.close()
        tunPfd = null
        setVpnState(VpnState.DISCONNECTED)
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
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
        if (vpnHandle >= 0L) Vpnlib.stop(vpnHandle)
        tunPfd?.close()
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
