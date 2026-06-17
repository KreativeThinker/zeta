package com.zeta.android.net

import android.util.Log
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.nio.ByteBuffer
import java.security.SecureRandom

object StunClient {

    private const val TAG = "ZetaStun"
    private const val STUN_HOST = "stun.l.google.com"
    private const val STUN_PORT = 19302
    private const val TIMEOUT_MS = 3000

    // Returns "ip:port" of the public endpoint, or null on failure.
    fun discoverEndpoint(localPort: Int = 0): String? {
        Log.d(TAG, "discoverEndpoint localPort=$localPort")
        return try {
            DatagramSocket(localPort).use { socket -> discoverWithSocket(socket) }
        } catch (e: Exception) {
            Log.w(TAG, "discoverEndpoint failed: ${e.message}")
            null
        }
    }

    // Use a pre-created socket (caller can call VpnService.protect() on it first).
    fun discoverWithSocket(socket: DatagramSocket): String? {
        Log.d(TAG, "STUN request via localPort=${socket.localPort} protected=${!socket.isBound.not()}")
        return try {
            socket.soTimeout = TIMEOUT_MS

            val transactionId = ByteArray(12).also { SecureRandom().nextBytes(it) }
            val request = buildBindingRequest(transactionId)
            // Force IPv4: pick an A record so the socket doesn't go out over IPv6
            val stunAddr = InetAddress.getAllByName(STUN_HOST)
                .firstOrNull { it is Inet4Address }
                ?: InetAddress.getByName(STUN_HOST)
            val server = InetSocketAddress(stunAddr, STUN_PORT)

            Log.d(TAG, "Sending binding request to $stunAddr:$STUN_PORT from port ${socket.localPort}")
            socket.send(DatagramPacket(request, request.size, server))

            val buf = ByteArray(512)
            val response = DatagramPacket(buf, buf.size)
            socket.receive(response)
            Log.d(TAG, "Received ${response.length} bytes from ${response.address}:${response.port}")

            val result = parseXorMappedAddress(buf, response.length, transactionId)
                ?.let { (ip, port) -> "$ip:$port" }
            Log.i(TAG, "XOR-MAPPED-ADDRESS → $result")
            result
        } catch (e: Exception) {
            Log.w(TAG, "STUN exchange failed: ${e.javaClass.simpleName}: ${e.message}")
            null
        }
    }

    private fun buildBindingRequest(txId: ByteArray): ByteArray {
        val buf = ByteBuffer.allocate(20)
        buf.putShort(0x0001)        // Binding Request
        buf.putShort(0)             // message length (no attributes)
        buf.putInt(0x2112A442)      // magic cookie
        buf.put(txId)
        return buf.array()
    }

    private fun parseXorMappedAddress(buf: ByteArray, len: Int, txId: ByteArray): Pair<String, Int>? {
        if (len < 20) return null
        val bb = ByteBuffer.wrap(buf, 0, len)
        bb.getShort() // type
        val msgLen = bb.getShort().toInt() and 0xFFFF
        bb.getInt()   // magic cookie (0x2112A442)
        bb.get(ByteArray(12)) // transaction id

        var remaining = msgLen
        while (remaining >= 4) {
            val attrType = bb.getShort().toInt() and 0xFFFF
            val attrLen = bb.getShort().toInt() and 0xFFFF
            remaining -= 4

            if (attrType == 0x0020 || attrType == 0x8020) {
                if (attrLen < 8) return null
                bb.get() // reserved
                val family = bb.get().toInt() and 0xFF
                val xport = (bb.getShort().toInt() and 0xFFFF) xor 0x2112

                return when (family) {
                    0x01 -> { // IPv4
                        val xip = bb.getInt() xor 0x2112A442.toInt()
                        val ip = "%d.%d.%d.%d".format(
                            (xip ushr 24) and 0xFF,
                            (xip ushr 16) and 0xFF,
                            (xip ushr 8) and 0xFF,
                            xip and 0xFF,
                        )
                        Pair(ip, xport)
                    }
                    0x02 -> { // IPv6 — XOR with magic cookie (4 bytes) || transaction ID (12 bytes)
                        if (attrLen < 20) return null
                        val xorKey = byteArrayOf(0x21, 0x12.toByte(), 0xa4.toByte(), 0x42) + txId
                        val addrBytes = ByteArray(16).also { bb.get(it) }
                        for (i in 0 until 16) addrBytes[i] = (addrBytes[i].toInt() xor xorKey[i].toInt()).toByte()
                        val groups = (0 until 8).map { i ->
                            ((addrBytes[i * 2].toInt() and 0xFF) shl 8) or (addrBytes[i * 2 + 1].toInt() and 0xFF)
                        }
                        val ip = groups.joinToString(":") { it.toString(16) }
                        Pair("[$ip]", xport) // brackets = WireGuard IPv6 endpoint format
                    }
                    else -> null
                }
            } else {
                val skip = (attrLen + 3) and 3.inv()
                if (skip > remaining) return null
                repeat(skip) { bb.get() }
                remaining -= skip
            }
        }
        return null
    }
}
