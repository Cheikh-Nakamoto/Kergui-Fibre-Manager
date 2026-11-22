package org.kergui.core

import android.app.NotificationManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.Uri
import kotlin.concurrent.thread

// "Accepter" on a new-device notification: the device is accepted under
// auto-reject (Max), so the watch service does not refuse it 15 min after it
// joined. Accepting is free. The core call runs off the main thread; the
// receiver stays alive until it is done.
class ApproveReceiver : BroadcastReceiver() {
  override fun onReceive(ctx: Context, intent: Intent) {
    val mac = intent.getStringExtra(EXTRA_MAC) ?: return
    val pending = goAsync()
    thread(name = "kergui-approve") {
      try {
        val cfg = WatchConfig.load(ctx)
        val core = CoreHolder.open(ctx, cfg.vaultKey)
        CoreHolder.configureIfNeeded(core, cfg.routerUrl, cfg.pin)
        val res = CoreHolder.request(core, "POST", "/api/devices/" + Uri.encode(mac) + "/accept", "")
        if (res.status.toInt() == 200) {
          ctx.getSystemService(NotificationManager::class.java).cancel(mac.hashCode())
          CoreHolder.note("info", "veille: $mac accepté depuis la notification")
        } else {
          CoreHolder.note("warn", "veille: accepter $mac a répondu ${res.status}")
        }
      } catch (e: Exception) {
        CoreHolder.note("warn", "veille: accepter $mac: ${e.message}")
      } finally {
        pending.finish()
      }
    }
  }

  companion object {
    const val ACTION = "org.kergui.core.APPROVE_DEVICE"
    const val EXTRA_MAC = "mac"
  }
}
