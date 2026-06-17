package com.zeta.android

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.zeta.android.ui.DashboardScreen
import com.zeta.android.ui.EnrollScreen
import com.zeta.android.ui.MeshWebViewScreen
import com.zeta.android.ui.PeersScreen
import com.zeta.android.ui.ServicesScreen
import com.zeta.android.ui.theme.ZetaTheme
import java.net.URLDecoder
import java.net.URLEncoder

class MainActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        val repo = (application as ZetaApplication).repository
        val okHttpClient = (application as ZetaApplication).okHttpClient

        setContent {
            ZetaTheme {
                val navController = rememberNavController()
                val nodeState by repo.nodeStateFlow.collectAsState()
                val startDest = if (nodeState != null) "dashboard" else "enroll"

                NavHost(navController = navController, startDestination = startDest) {
                    composable("enroll") {
                        EnrollScreen(
                            repository = repo,
                            onEnrolled = {
                                navController.navigate("dashboard") {
                                    popUpTo("enroll") { inclusive = true }
                                }
                            },
                        )
                    }
                    composable("dashboard") {
                        DashboardScreen(
                            repository = repo,
                            onNavigateToPeers = { navController.navigate("peers") },
                            onNavigateToServices = { navController.navigate("services") },
                            onForgotDevice = {
                                repo.forgetDevice()
                                navController.navigate("enroll") {
                                    popUpTo("dashboard") { inclusive = true }
                                }
                            },
                        )
                    }
                    composable("peers") {
                        PeersScreen(repository = repo)
                    }
                    composable("services") {
                        ServicesScreen(
                            repository = repo,
                            onOpenService = { meshIp, serviceName, peerHostname ->
                                val params = URLEncoder.encode("$meshIp|$serviceName|$peerHostname", "UTF-8")
                                navController.navigate("webview/$params")
                            },
                        )
                    }
                    composable("webview/{params}") { backStack ->
                        val raw = backStack.arguments?.getString("params")
                            ?.let { URLDecoder.decode(it, "UTF-8") }?.split("|")
                        if (raw?.size == 3) {
                            val meshDomain = nodeState?.domain?.substringAfter(".") ?: "mesh"
                            MeshWebViewScreen(
                                meshIp = raw[0],
                                serviceName = raw[1],
                                peerHostname = raw[2],
                                meshDomain = meshDomain,
                                okHttpClient = okHttpClient,
                                onBack = { navController.popBackStack() },
                            )
                        }
                    }
                }
            }
        }
    }
}
