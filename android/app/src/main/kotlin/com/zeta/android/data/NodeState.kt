package com.zeta.android.data

import kotlinx.serialization.Serializable

@Serializable
data class NodeState(
    val nodeId: String,
    val meshIp: String,
    val domain: String,
    val wgPrivateKey: String,
    val wgPublicKey: String,
    val certPem: String,
    val keyPem: String,
    val caPem: String,
    val coordinatorUrl: String,
)
