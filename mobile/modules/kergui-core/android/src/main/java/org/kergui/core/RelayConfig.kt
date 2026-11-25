package org.kergui.core

import android.content.Context

// What RelayService needs to run without the JS side: the vault key that
// opens the core (the relay's keys, hub token and the sealed box password
// stay encrypted in it) and the texts, already translated by the app. Kept
// in app-private storage; the app sets allowBackup=false.
data class RelayConfig(
  val enabled: Boolean,
  val vaultKey: String,
  val title: String,
  val channel: String,
  val stopText: String,
  val startingText: String,
  val activeText: String,
  val activeBoxOnlyText: String,
  val standbyText: String,
  val pausedText: String,
  val wrongNetworkText: String,
  val offlineText: String,
  val noWifiText: String,
  val noBoxText: String,
) {
  companion object {
    private const val PREFS = "kergui.relay"

    private fun prefs(ctx: Context) = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun load(ctx: Context): RelayConfig {
      val p = prefs(ctx)
      fun s(k: String, d: String) = p.getString(k, d) ?: d
      return RelayConfig(
        enabled = p.getBoolean("enabled", false),
        vaultKey = s("vaultKey", ""),
        title = s("title", "Kergui"),
        channel = s("channel", "Kergui"),
        stopText = s("stopText", "Stop"),
        startingText = s("startingText", ""),
        activeText = s("activeText", ""),
        activeBoxOnlyText = s("activeBoxOnlyText", ""),
        standbyText = s("standbyText", ""),
        // Saved by an app that predates #94: the standby text stands in.
        pausedText = s("pausedText", s("standbyText", "")),
        wrongNetworkText = s("wrongNetworkText", ""),
        offlineText = s("offlineText", ""),
        noWifiText = s("noWifiText", ""),
        noBoxText = s("noBoxText", ""),
      )
    }

    fun save(ctx: Context, values: Map<String, Any?>) {
      val e = prefs(ctx).edit()
      for ((k, v) in values) if (v is String) e.putString(k, v)
      e.putBoolean("enabled", true).apply()
    }

    fun disable(ctx: Context) {
      prefs(ctx).edit().putBoolean("enabled", false).remove("vaultKey").apply()
    }
  }
}
