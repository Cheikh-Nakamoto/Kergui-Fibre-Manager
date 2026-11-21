package org.kergui.core

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings
import expo.modules.kotlin.exception.CodedException
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import org.kergui.mobilecore.Core
import org.kergui.mobilecore.Mobilecore

class CoreNotOpenException : CodedException("ERR_CORE_NOT_OPEN", "Kergui core is not open: call open() first", null)

class CoreException(cause: Throwable) : CodedException("ERR_CORE", cause.message ?: "Kergui core error", cause)

// Bridges the gomobile-generated Go core (org.kergui.mobilecore) to JS. All
// router I/O happens in Go; AsyncFunctions run off the main thread. The core
// itself lives in CoreHolder, shared with the watch service.
class KerguiCoreModule : Module() {
  private fun core(): Core = CoreHolder.current() ?: throw CoreNotOpenException()

  private fun context(): Context = appContext.reactContext ?: throw CoreNotOpenException()

  private inline fun <T> wrap(block: () -> T): T =
    try {
      block()
    } catch (e: CodedException) {
      throw e
    } catch (e: Exception) {
      throw CoreException(e)
    }

  override fun definition() = ModuleDefinition {
    Name("KerguiCore")

    Function("version") { Mobilecore.version() }

    AsyncFunction("open") { vaultKey: String ->
      wrap { CoreHolder.open(context(), vaultKey) }
      Unit
    }

    AsyncFunction("configure") { routerUrl: String, adapterId: String, pinSha256: String ->
      wrap { CoreHolder.configure(core(), routerUrl, adapterId, pinSha256) }
    }

    AsyncFunction("request") { method: String, path: String, body: String ->
      val res = wrap { CoreHolder.request(core(), method, path, body) }
      mapOf("status" to res.status.toInt(), "body" to res.body)
    }

    // The paid-action gate inside the core (RFC-006): the hub keys it trusts
    // and the current entitlement token ("" for none).
    AsyncFunction("setPublicKeys") { keys: String ->
      wrap { core().setPublicKeys(keys) }
    }

    AsyncFunction("setEntitlement") { token: String ->
      wrap { core().setEntitlement(token) }
    }

    AsyncFunction("certificateFingerprint") { routerUrl: String ->
      wrap { Mobilecore.certificateFingerprint(routerUrl) }
    }

    // The journal screen. It reads the core's ring buffer rather than
    // Logcat: a household debugging a connection cannot open Logcat.
    AsyncFunction("journal") { since: Long, limit: Int ->
      wrap { core().journal(since, limit.toLong()) }
    }

    // One line from the app into the same journal. A core that is not open
    // yet has nowhere to keep it, so it only reaches Logcat.
    Function("note") { level: String, message: String ->
      CoreHolder.note(level, message)
    }

    AsyncFunction("startDemo") {
      CoreHolder.forgetConfiguration()
      wrap { core().startDemo() }
    }

    AsyncFunction("stopDemo") {
      CoreHolder.current()?.stopDemo()
    }

    // The background watch (WatchService). `config` carries the vault key,
    // the box and the translated texts; resetKnown makes the next reading a
    // silent first reading.
    AsyncFunction("startWatch") { config: Map<String, Any?>, resetKnown: Boolean ->
      val ctx = context()
      WatchConfig.save(ctx, config, resetKnown)
      WatchService.ensureChannels(ctx, WatchConfig.load(ctx))
      WatchService.start(ctx)
    }

    AsyncFunction("stopWatch") { forgetKnown: Boolean ->
      val ctx = context()
      WatchConfig.disable(ctx, forgetKnown)
      WatchService.stop(ctx)
    }

    Function("watchEnabled") {
      appContext.reactContext?.let { WatchConfig.load(it).enabled } ?: false
    }

    Function("batteryUnrestricted") {
      val ctx = appContext.reactContext ?: return@Function false
      (ctx.getSystemService(Context.POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(ctx.packageName)
    }

    // Asks the system to stop putting the app to sleep. ColorOS and other
    // vendor builds still kill foreground services of "optimised" apps.
    @SuppressLint("BatteryLife")
    AsyncFunction("requestBatteryUnrestricted") {
      val ctx = context()
      val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:" + ctx.packageName))
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
      ctx.startActivity(intent)
    }
  }
}
