package com.zeta.android.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
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
import androidx.compose.ui.unit.dp
import com.zeta.android.repository.ZetaRepository
import com.zeta.android.ui.theme.JetBrainsMonoFamily
import zeta.v1.NetworkMap

private data class ServiceItem(
    val peerHostname: String,
    val meshIp: String,
    val serviceName: String,
    val port: Int,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServicesScreen(
    repository: ZetaRepository,
    onOpenService: (meshIp: String, serviceName: String, peerHostname: String) -> Unit,
) {
    val networkMap: NetworkMap? by repository.networkMapFlow.collectAsState()
    val myPubKey = repository.nodeState?.wgPublicKey ?: ""

    val services = networkMap?.peersList?.flatMap { peer ->
        peer.servicesList
            .filter { svc -> myPubKey.isBlank() || svc.allowedPubkeysList.contains(myPubKey) }
            .map { svc -> ServiceItem(peer.hostname, peer.meshIp, svc.name, svc.port.toInt()) }
    } ?: emptyList()

    Scaffold(
        topBar = {
            TopAppBar(title = { Text("Services", style = MaterialTheme.typography.titleLarge) })
        },
    ) { padding ->
        if (services.isEmpty()) {
            Column(
                modifier = Modifier.fillMaxSize().padding(padding),
                verticalArrangement = Arrangement.Center,
                horizontalAlignment = androidx.compose.ui.Alignment.CenterHorizontally,
            ) {
                Text("No accessible services", style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text("Services you're allowed to access will appear here",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 12.dp),
            ) {
                items(services, key = { "${it.peerHostname}/${it.serviceName}" }) { svc ->
                    ServiceCard(
                        service = svc,
                        onOpen = { onOpenService(svc.meshIp, svc.serviceName, svc.peerHostname) },
                    )
                }
            }
        }
    }
}

@Composable
private fun ServiceCard(service: ServiceItem, onOpen: () -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
    ) {
        Row(
            modifier = Modifier.padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(service.serviceName, style = MaterialTheme.typography.titleSmall)
                Text(
                    "${service.peerHostname}.mesh",
                    style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text(
                    "${service.meshIp}:1080",
                    style = MaterialTheme.typography.labelSmall.copy(fontFamily = JetBrainsMonoFamily),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Button(
                onClick = onOpen,
                shape = RoundedCornerShape(8.dp),
            ) {
                Text("Open")
            }
        }
    }
}
