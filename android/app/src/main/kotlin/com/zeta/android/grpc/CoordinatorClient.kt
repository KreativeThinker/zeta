package com.zeta.android.grpc

import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import io.grpc.Metadata
import io.grpc.stub.MetadataUtils
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.SendChannel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.receiveAsFlow
import zeta.v1.CoordinatorServiceGrpcKt
import zeta.v1.RegisterRequest
import zeta.v1.RegisterResponse
import zeta.v1.SyncResponse
import zeta.v1.SyncUpdate

class CoordinatorClient(
    private val channel: ManagedChannel,
) {
    private val stub = CoordinatorServiceGrpcKt.CoordinatorServiceCoroutineStub(channel)

    suspend fun register(req: RegisterRequest): RegisterResponse = stub.register(req)

    fun openSyncStream(nodeId: String): Pair<SendChannel<SyncUpdate>, Flow<SyncResponse>> {
        val headers = Metadata().apply {
            put(Metadata.Key.of("node-id", Metadata.ASCII_STRING_MARSHALLER), nodeId)
        }
        val sendChannel = Channel<SyncUpdate>(capacity = 16)
        val responseFlow = stub
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(headers))
            .sync(sendChannel.receiveAsFlow())
        return Pair(sendChannel, responseFlow)
    }

    fun shutdown() {
        channel.shutdownNow()
    }

    companion object {
        fun create(
            host: String,
            port: Int,
        ): CoordinatorClient {
            val channel = ManagedChannelBuilder
                .forAddress(host, port)
                .usePlaintext()
                .build()
            return CoordinatorClient(channel)
        }

        fun parseUrl(url: String): Pair<String, Int> {
            val stripped =
                url
                    .removePrefix("grpc://")
                    .removePrefix("http://")
                    .removePrefix("https://")
            val parts = stripped.split(":")
            return Pair(parts[0], parts.getOrNull(1)?.toIntOrNull() ?: 50051)
        }
    }
}
