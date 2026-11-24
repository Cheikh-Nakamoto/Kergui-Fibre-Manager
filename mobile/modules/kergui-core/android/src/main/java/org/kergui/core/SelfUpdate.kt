package org.kergui.core

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import java.io.File
import java.io.FileInputStream
import java.security.MessageDigest

// The website APK keeps itself up to date (#65, #74). Everything an update
// needs that JS cannot do: whether this build may update itself, who
// installed it, the network, the file's SHA-256, the « install unknown
// apps » permission and handing the file to the system installer.
//
// The permission (REQUEST_INSTALL_PACKAGES), the flag below and the
// FileProvider are only in the manifest of the website APK: CI builds it with
// -Pkergui.selfUpdate=true (plugins/withSelfUpdate.js). The Play bundle has
// none of them, so allowed() is false there and installApk refuses.

/** The FileProvider the system installer reads the downloaded APK through. */
class UpdateFileProvider : FileProvider()

object SelfUpdate {
  private const val FLAG = "org.kergui.selfUpdate"
  private const val APK_MIME = "application/vnd.android.package-archive"

  fun allowed(ctx: Context): Boolean =
    try {
      val pm = ctx.packageManager
      val info = if (Build.VERSION.SDK_INT >= 33) {
        pm.getApplicationInfo(ctx.packageName, PackageManager.ApplicationInfoFlags.of(PackageManager.GET_META_DATA.toLong()))
      } else {
        @Suppress("DEPRECATION")
        pm.getApplicationInfo(ctx.packageName, PackageManager.GET_META_DATA)
      }
      info.metaData?.getBoolean(FLAG, false) == true
    } catch (e: Exception) {
      false
    }

  /** The package that installed the app ("com.android.vending" for Play), "" when unknown. */
  fun installer(ctx: Context): String =
    try {
      if (Build.VERSION.SDK_INT >= 30) {
        ctx.packageManager.getInstallSourceInfo(ctx.packageName).installingPackageName ?: ""
      } else {
        @Suppress("DEPRECATION")
        ctx.packageManager.getInstallerPackageName(ctx.packageName) ?: ""
      }
    } catch (e: Exception) {
      ""
    }

  /** The installed versionCode: the build number the hub compares. */
  fun build(ctx: Context): Long =
    try {
      val info = if (Build.VERSION.SDK_INT >= 33) {
        ctx.packageManager.getPackageInfo(ctx.packageName, PackageManager.PackageInfoFlags.of(0L))
      } else {
        @Suppress("DEPRECATION")
        ctx.packageManager.getPackageInfo(ctx.packageName, 0)
      }
      if (Build.VERSION.SDK_INT >= 28) {
        info.longVersionCode
      } else {
        @Suppress("DEPRECATION")
        info.versionCode.toLong()
      }
    } catch (e: Exception) {
      0L
    }

  /** An unmetered network (Wi-Fi, not a phone's hotspot): a download costs no data. */
  fun unmetered(ctx: Context): Boolean {
    val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager ?: return false
    if (cm.activeNetwork == null) return false
    return !cm.isActiveNetworkMetered
  }

  /** Before Android 8 the system installer asks for unknown sources itself. */
  fun canInstall(ctx: Context): Boolean =
    Build.VERSION.SDK_INT < 26 || ctx.packageManager.canRequestPackageInstalls()

  /** The « install unknown apps » page for this app (vendors such as ColorOS may lack it). */
  fun openInstallSettings(ctx: Context) {
    val pkg = Uri.parse("package:" + ctx.packageName)
    val first = if (Build.VERSION.SDK_INT >= 26) {
      Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, pkg)
    } else {
      Intent(Settings.ACTION_SECURITY_SETTINGS)
    }
    try {
      ctx.startActivity(first.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (e: ActivityNotFoundException) {
      ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, pkg).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
  }

  /** A file:// URI (expo-file-system) or a plain path. */
  fun fileOf(uriOrPath: String): File =
    if (uriOrPath.startsWith("file:")) File(Uri.parse(uriOrPath).path ?: "") else File(uriOrPath)

  /** Lower-case hex SHA-256 of the file, streamed: the APK never sits in memory. */
  fun sha256(file: File): String {
    val md = MessageDigest.getInstance("SHA-256")
    FileInputStream(file).use { input ->
      val buf = ByteArray(64 * 1024)
      while (true) {
        val n = input.read(buf)
        if (n < 0) break
        md.update(buf, 0, n)
      }
    }
    return md.digest().joinToString("") { "%02x".format(it) }
  }

  /** Hands the file to the system installer, which always asks the household to confirm. */
  fun install(ctx: Context, file: File) {
    val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".kergui.update", file)
    val intent = Intent(Intent.ACTION_VIEW)
      .setDataAndType(uri, APK_MIME)
      .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
    ctx.startActivity(intent)
  }
}
