package com.zeta.android.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.zeta.android.repository.ZetaRepository
import kotlinx.coroutines.launch

@Composable
fun EnrollScreen(repository: ZetaRepository, onEnrolled: () -> Unit) {
    var url by remember { mutableStateOf("") }
    var key by remember { mutableStateOf("") }
    var loading by remember { mutableStateOf(false) }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()

    Scaffold(snackbarHost = { SnackbarHost(snackbar) }) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(horizontal = 24.dp),
            verticalArrangement = Arrangement.Center,
        ) {
            Text("Zeta", style = MaterialTheme.typography.displayLarge)
            Text("Connect to your mesh", style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant)

            Spacer(Modifier.height(40.dp))

            OutlinedTextField(
                value = url,
                onValueChange = { url = it },
                label = { Text("Coordinator URL") },
                placeholder = { Text("grpc://vpn.example.com:50051") },
                modifier = Modifier.fillMaxWidth(),
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Uri,
                    imeAction = ImeAction.Next,
                ),
                singleLine = true,
            )

            Spacer(Modifier.height(16.dp))

            OutlinedTextField(
                value = key,
                onValueChange = { key = it },
                label = { Text("Preauth Key") },
                modifier = Modifier.fillMaxWidth(),
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Password,
                    imeAction = ImeAction.Done,
                ),
                singleLine = true,
            )

            Spacer(Modifier.height(24.dp))

            Button(
                onClick = {
                    if (url.isBlank() || key.isBlank()) {
                        scope.launch { snackbar.showSnackbar("Enter a coordinator URL and preauth key") }
                        return@Button
                    }
                    loading = true
                    scope.launch {
                        runCatching { repository.enroll(url.trim(), key.trim()) }
                            .onSuccess { onEnrolled() }
                            .onFailure { e ->
                                loading = false
                                snackbar.showSnackbar(e.message ?: "Enrollment failed")
                            }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
                enabled = !loading,
            ) {
                if (loading) {
                    CircularProgressIndicator(modifier = Modifier.height(20.dp), strokeWidth = 2.dp)
                } else {
                    Text("Connect")
                }
            }
        }
    }
}
