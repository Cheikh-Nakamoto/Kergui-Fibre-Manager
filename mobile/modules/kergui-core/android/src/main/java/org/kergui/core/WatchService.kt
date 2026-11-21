package org.kergui.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import org.json.JSONArray
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

// Keeps watching the box while the app is closed: every `interval` seconds it
// reads the device list through the Go core and posts a notification for each
// MAC it has not seen before.
//
// A foreground service is the only way Android keeps doing this every few
// seconds: WorkManager (expo-background-task) runs at most every 15 minutes,
// and far less on ColorOS. The ongoing notification is the price.
class WatchService : Service() {
  private var executor: ScheduledExecutorService? = null
  private var wakeLock: PowerManager.WakeLock? = null

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onCreate() {
    super.onCreate()
    val cfg = WatchConfig.load(this)
    ensureChannels(this, cfg)
    val notif = ongoing(cfg.ongoingText)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
      startForeground(ONGOING_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
    } else {
      startForeground(ONGOING_ID, notif)
    }
    // Without it the CPU sleeps with the screen off and the timer stretches
    // to minutes. The work itself is one small HTTPS exchange per interval.
    wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
      .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "kergui:watch")
      .apply { setReferenceCounted(false); acquire() }
  }

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    val cfg = WatchConfig.load(this)
    if (!cfg.enabled || cfg.vaultKey.isEmpty() || cfg.routerUrl.isEmpty()) {
      stopSelf()
      return START_NOT_STICKY
    }
    executor?.shutdownNow()
    executor = Executors.newSingleThreadScheduledExecutor().also {
      it.scheduleWithFixedDelay({ tick() }, 0, cfg.intervalSec.toLong(), TimeUnit.SECONDS)
    }
    CoreHolder.note("info", "veille: démarrée, contrôle toutes les ${cfg.intervalSec} s")
    // Restarted by the system with a null intent if it kills the process.
    return START_STICKY
  }

  override fun onDestroy() {
    executor?.shutdownNow()
    executor = null
    wakeLock?.let { if (it.isHeld) it.release() }
    CoreHolder.note("info", "veille: arrêtée")
    super.onDestroy()
  }

  private fun tick() {
    val cfg = WatchConfig.load(this)
    try {
      val core = CoreHolder.open(this, cfg.vaultKey)
      CoreHolder.configureIfNeeded(core, cfg.routerUrl, cfg.pin)
      val res = CoreHolder.request(core, "GET", "/api/devices", "")
      if (res.status.toInt() != 200) {
        CoreHolder.note("warn", "veille: /api/devices a répondu ${res.status}")
        updateOngoing(cfg, cfg.ongoingErrorText)
        return
      }
      val devices = JSONArray(res.body)
      val known = cfg.known
      val fresh = mutableListOf<Pair<String, String>>()
      val next = HashSet(known ?: emptySet())
      var connected = 0
      for (i in 0 until devices.length()) {
        val d = devices.getJSONObject(i)
        val mac = d.optString("MAC")
        if (mac.isEmpty()) continue
        if (d.optBoolean("Connected", true)) connected++
        if (known != null && mac !in known) fresh += mac to displayName(d.optString("CustomName"), d.optString("Hostname"), mac)
        next += mac
      }
      // First reading for this box: everything there is recorded silently,
      // otherwise turning the alerts on would ring once per existing device.
      WatchConfig.saveKnown(this, next)
      for ((mac, name) in fresh) notifyNewDevice(cfg, mac, name)
      if (fresh.isNotEmpty()) CoreHolder.note("info", "veille: ${fresh.size} nouvel(s) appareil(s)")
      val time = SimpleDateFormat("HH:mm", Locale.FRANCE).format(Date())
      updateOngoing(cfg, cfg.ongoingCountText.replace("{count}", connected.toString()).replace("{time}", time))
    } catch (e: Exception) {
      // Off the home Wi-Fi the box is unreachable: keep trying, quietly.
      CoreHolder.note("warn", "veille: ${e.message}")
      updateOngoing(cfg, cfg.ongoingErrorText)
    }
  }

  private fun displayName(custom: String, host: String, mac: String) =
    custom.ifBlank { host.ifBlank { mac } }

  private fun openApp(path: String?): PendingIntent {
    val intent = if (path != null) {
      Intent(Intent.ACTION_VIEW, Uri.parse("kergui://$path")).setPackage(packageName)
    } else {
      packageManager.getLaunchIntentForPackage(packageName) ?: Intent()
    }
    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
    return PendingIntent.getActivity(this, path?.hashCode() ?: 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
  }

  private fun ongoing(text: String): Notification =
    Notification.Builder(this, ONGOING_CHANNEL)
      .setSmallIcon(iconRes())
      .setContentTitle(WatchConfig.load(this).ongoingTitle)
      .setContentText(text)
      .setOngoing(true)
      .setOnlyAlertOnce(true)
      .setContentIntent(openApp(null))
      .build()

  private fun updateOngoing(cfg: WatchConfig, text: String) {
    val nm = getSystemService(NotificationManager::class.java)
    nm.notify(ONGOING_ID, ongoing(text))
  }

  private fun notifyNewDevice(cfg: WatchConfig, mac: String, name: String) {
    val n = Notification.Builder(this, ALERT_CHANNEL)
      .setSmallIcon(iconRes())
      .setContentTitle(cfg.alertTitle)
      .setContentText(cfg.alertBody.replace("{name}", name).replace("{mac}", mac))
      .setAutoCancel(true)
      .setCategory(Notification.CATEGORY_STATUS)
      .setContentIntent(openApp("device/" + Uri.encode(mac)))
      .build()
    getSystemService(NotificationManager::class.java).notify(mac.hashCode(), n)
  }

  private fun iconRes(): Int {
    val id = resources.getIdentifier("notification_icon", "drawable", packageName)
    return if (id != 0) id else applicationInfo.icon
  }

  companion object {
    const val ONGOING_ID = 4201
    const val ONGOING_CHANNEL = "watch"
    // Same id as the JS side (expo-notifications), so both share one setting.
    const val ALERT_CHANNEL = "new-devices"

    fun ensureChannels(ctx: Context, cfg: WatchConfig) {
      val nm = ctx.getSystemService(NotificationManager::class.java)
      nm.createNotificationChannel(NotificationChannel(ONGOING_CHANNEL, cfg.ongoingChannel, NotificationManager.IMPORTANCE_LOW))
      if (nm.getNotificationChannel(ALERT_CHANNEL) == null) {
        nm.createNotificationChannel(NotificationChannel(ALERT_CHANNEL, cfg.alertChannel, NotificationManager.IMPORTANCE_HIGH))
      }
    }

    fun start(ctx: Context) {
      val i = Intent(ctx, WatchService::class.java)
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ctx.startForegroundService(i) else ctx.startService(i)
    }

    fun stop(ctx: Context) {
      ctx.stopService(Intent(ctx, WatchService::class.java))
    }
  }
}
