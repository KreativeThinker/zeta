package com.zeta.android.data

import android.content.Context
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

class StateManager(context: Context) {

    private val masterKey = MasterKey.Builder(context)
        .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
        .build()

    private val prefs = EncryptedSharedPreferences.create(
        context,
        "zeta_state",
        masterKey,
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    fun saveNodeState(state: NodeState) {
        prefs.edit()
            .putString(KEY_NODE_STATE, Json.encodeToString(state))
            .apply()
    }

    fun loadNodeState(): NodeState? {
        val json = prefs.getString(KEY_NODE_STATE, null) ?: return null
        return try {
            Json.decodeFromString<NodeState>(json)
        } catch (_: Exception) {
            null
        }
    }

    fun clearNodeState() {
        prefs.edit().remove(KEY_NODE_STATE).apply()
    }

    companion object {
        private const val KEY_NODE_STATE = "node_state_v1"
    }
}
