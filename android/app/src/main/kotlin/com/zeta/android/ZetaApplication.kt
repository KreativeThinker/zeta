package com.zeta.android

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import com.wireguard.android.backend.GoBackend
import com.zeta.android.data.StateManager
import com.zeta.android.net.MeshDnsResolver
import com.zeta.android.repository.ZetaRepository
import com.zeta.android.vpn.ZetaVpnService
import okhttp3.OkHttpClient

class ZetaApplication : Application() {

    lateinit var repository: ZetaRepository
    lateinit var okHttpClient: OkHttpClient
    lateinit var wgBackend: GoBackend
    val meshDnsResolver = MeshDnsResolver()

    override fun onCreate() {
        super.onCreate()
        // GoBackend must exist before ZetaVpnService starts so that
        // GoBackend.VpnService.onCreate() can register itself with the backend.
        wgBackend = GoBackend(this)
        createNotificationChannel()
        val stateManager = StateManager(this)
        okHttpClient = OkHttpClient.Builder()
            .dns(meshDnsResolver)
            .build()
        repository = ZetaRepository(stateManager, meshDnsResolver)
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            ZetaVpnService.CHANNEL_ID,
            "Zeta VPN",
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = "Zeta mesh VPN status"
        }
        getSystemService(NotificationManager::class.java)
            .createNotificationChannel(channel)
    }
}
