package org.kergui.core

import android.content.Context
import android.util.Log
import org.kergui.mobilecore.Core
import org.kergui.mobilecore.Logger
import org.kergui.mobilecore.Mobilecore
import org.kergui.mobilecore.Response
import java.io.File

// One Go core per process, shared by the JS module and the watch service. It
// owns the SQLite file and the router session.
//
// Every request goes through ioLock: the box keeps a single admin session and
// guards each data page with the view loaded just before it, so two requests
// running at once (the screen refreshing while the service polls) knock each
// other out with SessionTimeout.
object CoreHolder {
  const val TAG = "KerguiCore"

  private val openLock = Any()
  private val ioLock = Any()

  @Volatile private var shared: Core? = null

  // Router the core currently points at, so the service does not rebuild the
  // handler (and drop the session) on every start.
  @Volatile private var configured: String? = null

  val logger = object : Logger {
    override fun log(level: String, message: String) {
      when (level) {
        "error" -> Log.e(TAG, message)
        "warn" -> Log.w(TAG, message)
        "info" -> Log.i(TAG, message)
        else -> Log.d(TAG, message)
      }
    }
  }

  fun current(): Core? = shared

  fun open(ctx: Context, vaultKey: String): Core =
    synchronized(openLock) {
      shared ?: Mobilecore.open(File(ctx.noBackupFilesDir, "kergui").absolutePath, vaultKey, logger).also { shared = it }
    }

  fun configure(core: Core, routerUrl: String, adapterId: String, pin: String) {
    synchronized(ioLock) {
      core.configure(routerUrl, adapterId, pin)
      configured = "$routerUrl|$adapterId|$pin"
    }
  }

  fun configureIfNeeded(core: Core, routerUrl: String, pin: String) {
    if (configured != "$routerUrl||$pin") configure(core, routerUrl, "", pin)
  }

  /** Points at something else (demo), so the next service start configures again. */
  fun forgetConfiguration() {
    configured = null
  }

  fun request(core: Core, method: String, path: String, body: String): Response =
    synchronized(ioLock) { core.request(method, path, body) }

  fun note(level: String, message: String) {
    val c = shared
    if (c != null) c.note(level, message) else logger.log(level, message)
  }
}
