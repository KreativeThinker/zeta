package com.zeta.android.ui

import android.content.Intent
import android.net.VpnService
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.zeta.android.repository.ZetaRepository
import com.zeta.android.ui.theme.JetBrainsMonoFamily
import com.zeta.android.ui.theme.ZetaDanger
import com.zeta.android.ui.theme.ZetaOnline
import com.zeta.android.vpn.ZetaVpnService
import zeta.v1.NetworkMap

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DashboardScreen(
    repository: ZetaRepository,
    onNavigateToPeers: () -> Unit,
    onNavigateToServices: () -> Unit,
    onForgotDevice: () -> Unit,
) {
    val context = LocalContext.current
    val nodeState by repository.nodeStateFlow.collectAsState()
    val networkMap: NetworkMap? by repository.networkMapFlow.collectAsState()
    var vpnConnected by remember { mutableStateOf(false) }

    val vpnPermLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK) {
            context.startForegroundService(
                Intent(context, ZetaVpnService::class.java).setAction(ZetaVpnService.ACTION_START),
            )
            vpnConnected = true
        }
    }

    val onlinePeers = networkMap?.peersList?.count { it.online } ?: 0
    val totalPeers = networkMap?.peersList?.size ?: 0

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Zeta", style = MaterialTheme.typography.titleLarge) },
                actions = {
                    TextButton(onClick = onForgotDevice) {
                        Text("Forget Device", color = ZetaDanger, style = MaterialTheme.typography.labelLarge)
                    }
                },
            )
        },
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = false,
                    onClick = onNavigateToPeers,
                    icon = {},
                    label = { Text("Peers") },
                )
                NavigationBarItem(
                    selected = false,
                    onClick = onNavigateToServices,
                    icon = {},
                    label = { Text("Services") },
                )
            }
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Spacer(Modifier.height(8.dp))

            // Status card
            Card(
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(16.dp),
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            ) {
                Column(modifier = Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        androidx.compose.foundation.Canvas(modifier = Modifier.size(8.dp)) {
                            drawCircle(if (vpnConnected) ZetaOnline else Color(0xFFAAAAAA))
                        }
                        Spacer(Modifier.width(8.dp))
                        Text(
                            if (vpnConnected) "Connected" else "Disconnected",
                            style = MaterialTheme.typography.titleSmall,
                        )
                    }
                    nodeState?.meshIp?.let { ip ->
                        Text(ip, style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    nodeState?.domain?.let { domain ->
                        Text(domain, style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }

            // Stats row
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                StatCard(label = "Online Peers", value = "$onlinePeers", modifier = Modifier.weight(1f))
                StatCard(label = "Total Peers", value = "$totalPeers", modifier = Modifier.weight(1f))
            }

            // VPN toggle
            if (vpnConnected) {
                Button(
                    onClick = {
                        context.startService(
                            Intent(context, ZetaVpnService::class.java).setAction(ZetaVpnService.ACTION_STOP),
                        )
                        vpnConnected = false
                    },
                    modifier = Modifier.fillMaxWidth(),
                    colors = ButtonDefaults.buttonColors(containerColor = ZetaDanger),
                ) {
                    Text("Disconnect")
                }
            } else {
                Button(
                    onClick = {
                        val prepareIntent = VpnService.prepare(context)
                        if (prepareIntent != null) {
                            vpnPermLauncher.launch(prepareIntent)
                        } else {
                            context.startForegroundService(
                                Intent(context, ZetaVpnService::class.java).setAction(ZetaVpnService.ACTION_START),
                            )
                            vpnConnected = true
                        }
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("Connect to Mesh")
                }
            }
        }
    }
}

@Composable
private fun StatCard(label: String, value: String, modifier: Modifier = Modifier) {
    Card(
        modifier = modifier,
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(label.uppercase(), style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(value, style = MaterialTheme.typography.displayLarge.copy(fontSize = MaterialTheme.typography.titleLarge.fontSize))
        }
    }
}
