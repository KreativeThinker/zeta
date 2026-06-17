package com.zeta.android.data

import com.wireguard.crypto.KeyPair

data class WireGuardKeyPair(
    val privateKey: String,
    val publicKey: String,
) {
    companion object {
        fun generate(): WireGuardKeyPair {
            val kp = KeyPair()
            return WireGuardKeyPair(
                privateKey = kp.privateKey.toBase64(),
                publicKey = kp.publicKey.toBase64(),
            )
        }
    }
}
