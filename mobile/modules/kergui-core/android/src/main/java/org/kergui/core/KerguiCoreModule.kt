package org.kergui.core

import android.util.Log
import expo.modules.kotlin.exception.CodedException
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import org.kergui.mobilecore.Core
import org.kergui.mobilecore.Logger
import org.kergui.mobilecore.Mobilecore
import java.io.File

class CoreNotOpenException : CodedException("ERR_CORE_NOT_OPEN", "Kergui core is not open: call open() first", null)

class CoreException(cause: Throwable) : CodedException("ERR_CORE", cause.message ?: "Kergui core error", cause)

// Bridges the gomobile-generated Go core (org.kergui.mobilecore) to JS. All
// router I/O happens in Go; AsyncFunctions run off the main thread.
class KerguiCoreModule : Module() {
  private val logger = object : Logger {
    override fun log(level: String, message: String) {
      when (level) {
        "error" -> Log.e(TAG, message)
        "warn" -> Log.w(TAG, message)
        "info" -> Log.i(TAG, message)
        else -> Log.d(TAG, message)
      }
    }
  }

  private fun core(): Core = shared ?: throw CoreNotOpenException()

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
      synchronized(lock) {
        if (shared == null) {
          val ctx = appContext.reactContext ?: throw CoreNotOpenException()
          val dir = File(ctx.noBackupFilesDir, "kergui")
          shared = wrap { Mobilecore.open(dir.absolutePath, vaultKey, logger) }
        }
      }
    }

    AsyncFunction("configure") { routerUrl: String, adapterId: String, pinSha256: String ->
      wrap { core().configure(routerUrl, adapterId, pinSha256) }
    }

    AsyncFunction("request") { method: String, path: String, body: String ->
      val res = wrap { core().request(method, path, body) }
      mapOf("status" to res.status.toInt(), "body" to res.body)
    }

    AsyncFunction("certificateFingerprint") { routerUrl: String ->
      wrap { Mobilecore.certificateFingerprint(routerUrl) }
    }

    AsyncFunction("startDemo") {
      wrap { core().startDemo() }
    }

    AsyncFunction("stopDemo") {
      shared?.stopDemo()
    }
  }

  companion object {
    private const val TAG = "KerguiCore"
    private val lock = Any()

    // One core per process: it owns the SQLite file and the single router
    // session, and survives JS reloads.
    @Volatile private var shared: Core? = null
  }
}
