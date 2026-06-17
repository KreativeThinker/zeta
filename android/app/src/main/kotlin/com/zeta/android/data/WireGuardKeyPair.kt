package com.zeta.android.data

import vpnlib.Vpnlib

data class WireGuardKeyPair(
    val privateKey: String,
    val publicKey: String,
) {
    companion object {
        fun generate(): WireGuardKeyPair {
            val priv = Vpnlib.generatePrivateKey()
            val pub = Vpnlib.publicKey(priv)
            return WireGuardKeyPair(privateKey = priv, publicKey = pub)
        }
    }
}
