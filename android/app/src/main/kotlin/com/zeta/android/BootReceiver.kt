package com.zeta.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.zeta.android.data.StateManager
import com.zeta.android.vpn.ZetaVpnService

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            if (StateManager(context).loadNodeState() != null) {
                context.startForegroundService(
                    Intent(context, ZetaVpnService::class.java)
                        .setAction(ZetaVpnService.ACTION_START),
                )
            }
        }
    }
}
