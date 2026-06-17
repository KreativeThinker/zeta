package com.zeta.android.ui

import androidx.compose.foundation.Canvas
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.zeta.android.repository.ZetaRepository
import com.zeta.android.ui.theme.JetBrainsMonoFamily
import zeta.v1.NetworkMap
import com.zeta.android.ui.theme.ZetaOnline

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeersScreen(repository: ZetaRepository) {
    val networkMap: NetworkMap? by repository.networkMapFlow.collectAsState()
    val peers = networkMap?.peersList ?: emptyList()

    Scaffold(
        topBar = {
            TopAppBar(title = { Text("Peers", style = MaterialTheme.typography.titleLarge) })
        },
    ) { padding ->
        if (peers.isEmpty()) {
            Column(
                modifier = Modifier.fillMaxSize().padding(padding),
                verticalArrangement = Arrangement.Center,
                horizontalAlignment = androidx.compose.ui.Alignment.CenterHorizontally,
            ) {
                Text("No peers yet", style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text("Connect to the mesh to see peers", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 12.dp),
            ) {
                items(peers, key = { it.nodeId }) { peer ->
                    PeerCard(
                        hostname = peer.hostname,
                        meshIp = peer.meshIp,
                        endpoint = peer.endpoint,
                        online = peer.online,
                        serviceCount = peer.servicesCount,
                    )
                }
            }
        }
    }
}

@Composable
private fun PeerCard(
    hostname: String,
    meshIp: String,
    endpoint: String,
    online: Boolean,
    serviceCount: Int,
) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Canvas(modifier = Modifier.size(8.dp)) {
                    drawCircle(if (online) ZetaOnline else Color(0xFFAAAAAA))
                }
                Spacer(Modifier.width(8.dp))
                Text(hostname, style = MaterialTheme.typography.titleSmall)
                Spacer(Modifier.weight(1f))
                if (serviceCount > 0) {
                    Text("$serviceCount service${if (serviceCount != 1) "s" else ""}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Text(meshIp, style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (endpoint.isNotBlank()) {
                Text(endpoint, style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}
