package org.kergui.core

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

// Restarts the watch and the relay after a reboot or an app update, if they
// were on.
class BootReceiver : BroadcastReceiver() {
  override fun onReceive(ctx: Context, intent: Intent) {
    when (intent.action) {
      Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED ->
        {
          if (WatchConfig.load(ctx).enabled) WatchService.start(ctx)
          if (RelayConfig.load(ctx).enabled) RelayService.start(ctx)
        }
    }
  }
}
