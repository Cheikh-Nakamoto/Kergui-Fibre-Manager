package org.kergui.core

import android.content.Context
import org.json.JSONArray

// What the service needs to run without the JS side: the vault key that opens
// the core's database (the router password stays encrypted in it), the box
// address and certificate pin, and the texts, already translated by the app.
// Kept in app-private storage; the app sets allowBackup=false.
data class WatchConfig(
  val enabled: Boolean,
  val vaultKey: String,
  val routerUrl: String,
  val pin: String,
  val intervalSec: Int,
  val known: Set<String>?,
  val ongoingTitle: String,
  val ongoingText: String,
  val ongoingCountText: String,
  val ongoingErrorText: String,
  val ongoingChannel: String,
  val alertTitle: String,
  val alertBody: String,
  val alertChannel: String,
  // Auto-reject (Max): the action on a new-device notification, and the
  // notification of a device the rule refused.
  val approveText: String,
  val refusedTitle: String,
  val refusedBody: String,
) {
  companion object {
    private const val PREFS = "kergui.watch"

    private fun prefs(ctx: Context) = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun load(ctx: Context): WatchConfig {
      val p = prefs(ctx)
      val known = p.getString("known", null)?.let { raw ->
        runCatching { JSONArray(raw).let { a -> (0 until a.length()).map { a.getString(it) }.toSet() } }.getOrNull()
      }
      return WatchConfig(
        enabled = p.getBoolean("enabled", false),
        vaultKey = p.getString("vaultKey", "") ?: "",
        routerUrl = p.getString("routerUrl", "") ?: "",
        pin = p.getString("pin", "") ?: "",
        intervalSec = p.getInt("intervalSec", 30).coerceAtLeast(10),
        known = known,
        ongoingTitle = p.getString("ongoingTitle", "Kergui") ?: "Kergui",
        ongoingText = p.getString("ongoingText", "") ?: "",
        ongoingCountText = p.getString("ongoingCountText", "{count} · {time}") ?: "",
        ongoingErrorText = p.getString("ongoingErrorText", "") ?: "",
        ongoingChannel = p.getString("ongoingChannel", "Kergui") ?: "Kergui",
        alertTitle = p.getString("alertTitle", "") ?: "",
        alertBody = p.getString("alertBody", "{name} ({mac})") ?: "",
        alertChannel = p.getString("alertChannel", "Kergui") ?: "Kergui",
        approveText = p.getString("approveText", "OK") ?: "OK",
        refusedTitle = p.getString("refusedTitle", "") ?: "",
        refusedBody = p.getString("refusedBody", "{name} ({mac})") ?: "",
      )
    }

    fun save(ctx: Context, values: Map<String, Any?>, resetKnown: Boolean) {
      val e = prefs(ctx).edit()
      for ((k, v) in values) {
        when (v) {
          is String -> e.putString(k, v)
          is Boolean -> e.putBoolean(k, v)
          is Number -> e.putInt(k, v.toInt())
        }
      }
      e.putBoolean("enabled", true)
      if (resetKnown) e.remove("known")
      e.apply()
    }

    fun saveKnown(ctx: Context, known: Set<String>) {
      prefs(ctx).edit().putString("known", JSONArray(known.toList()).toString()).apply()
    }

    fun disable(ctx: Context, forget: Boolean) {
      val e = prefs(ctx).edit().putBoolean("enabled", false).remove("vaultKey")
      if (forget) e.remove("known")
      e.apply()
    }
  }
}
