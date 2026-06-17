package com.zeta.android.net

import okhttp3.Dns
import zeta.v1.Peer
import java.net.InetAddress
import java.net.UnknownHostException
import java.util.concurrent.ConcurrentHashMap

class MeshDnsResolver(
    private val fallback: Dns = Dns.SYSTEM,
) : Dns {
    private val records = ConcurrentHashMap<String, String>()

    fun updateFromPeers(
        peers: List<Peer>,
        meshDomain: String,
    ) {
        val updated = mutableMapOf<String, String>()
        for (peer in peers) {
            if (peer.hostname.isBlank() || peer.meshIp.isBlank()) continue
            val host = "${peer.hostname}.$meshDomain"
            updated[host] = peer.meshIp
            for (svc in peer.servicesList) {
                updated["${svc.name}.${peer.hostname}.$meshDomain"] = peer.meshIp
            }
        }
        records.clear()
        records.putAll(updated)
    }

    override fun lookup(hostname: String): List<InetAddress> {
        val ip = records[hostname]
        if (ip != null) return listOf(InetAddress.getByName(ip))
        // Only throw NXDOMAIN for *.mesh hostnames; let others through to system
        if (hostname.contains(".mesh")) throw UnknownHostException("No mesh record: $hostname")
        return fallback.lookup(hostname)
    }
}
