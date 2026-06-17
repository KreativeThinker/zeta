package com.zeta.android.ui

import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import android.util.Log
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.ByteArrayInputStream

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MeshWebViewScreen(
    meshIp: String,
    serviceName: String,
    peerHostname: String,
    meshDomain: String,
    okHttpClient: OkHttpClient,
    onBack: () -> Unit,
) {
    // Use the mesh hostname as the URL authority so that:
    // 1. OkHttp resolves it via MeshDnsResolver (hostname → mesh IP)
    // 2. The Host header is set automatically from the URL (proxy routes by Host)
    // 3. The network security config permits cleartext for *.mesh
    val meshHost = "$serviceName.$peerHostname.$meshDomain"
    val targetBase = "http://$meshHost:1080"

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    androidx.compose.material3.Text(
                        meshHost,
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            AndroidView(
                modifier = Modifier.fillMaxSize(),
                factory = { ctx ->
                    WebView(ctx).apply {
                        settings.javaScriptEnabled = true
                        settings.domStorageEnabled = true
                        settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
                        webViewClient = object : WebViewClient() {
                            override fun shouldInterceptRequest(
                                view: WebView,
                                request: WebResourceRequest,
                            ): WebResourceResponse? {
                                // Only intercept requests destined for mesh hosts.
                                // External resources (CDN fonts, analytics, etc.) pass through
                                // to the WebView's native HTTP stack — intercepting them would
                                // proxy them to the mesh agent (wrong) and cause 502 retry loops.
                                val requestHost = request.url.host ?: return null
                                if (!requestHost.endsWith(".mesh")) return null

                                return runCatching {
                                    // Build the string directly — Uri.Builder.authority()
                                    // encodes the colon in "host:port" as %3A, which OkHttp rejects.
                                    val path = request.url.path?.takeIf { it.isNotEmpty() } ?: "/"
                                    val query = request.url.query?.let { "?$it" } ?: ""
                                    val rewritten = "http://$meshHost:1080$path$query"

                                    val reqBuilder = Request.Builder().url(rewritten)
                                    request.requestHeaders.forEach { (k, v) ->
                                        if (!k.equals("Host", ignoreCase = true)) {
                                            reqBuilder.addHeader(k, v)
                                        }
                                    }
                                    // Host header is derived from the URL authority by OkHttp.

                                    val response = okHttpClient.newCall(reqBuilder.build()).execute()
                                    val contentType = response.header("Content-Type", "text/html") ?: "text/html"
                                    val mimeType = contentType.substringBefore(";").trim()
                                    val encoding = contentType.substringAfter("charset=", "utf-8").trim()
                                    val responseHeaders = response.headers.toMultimap()
                                        .mapValues { it.value.joinToString(", ") }

                                    WebResourceResponse(
                                        mimeType,
                                        encoding,
                                        response.code,
                                        response.message.ifBlank { "OK" },
                                        responseHeaders,
                                        response.body?.byteStream(),
                                    )
                                }.getOrElse { e ->
                                    Log.e("MeshWebView", "Request to ${request.url} failed", e)
                                    val body = "Mesh proxy error: ${e.javaClass.simpleName}: ${e.message}"
                                    WebResourceResponse(
                                        "text/plain", "utf-8", 502, "Bad Gateway",
                                        emptyMap(),
                                        ByteArrayInputStream(body.toByteArray()),
                                    )
                                }
                            }
                        }
                        loadUrl(targetBase)
                    }
                },
            )
        }
    }
}
