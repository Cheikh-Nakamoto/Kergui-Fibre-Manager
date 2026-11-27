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
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

// Keeps watching the box while the app is closed: every `interval` seconds it
// reads the device list through the Go core and posts a notification for each
// device nobody approved that arrived since the last reading (#58), with
// « Approuver ». It also runs one round of auto-reject (Max), whose setting
// lives in the core: a device the rule refused gets its own notification.
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
      // Issue #58: what was on the Wi-Fi at the last reading. A device that
      // was not, is now, and nobody approved rings; one that stays does not.
      val previous = cfg.known?.map { it.lowercase(Locale.ROOT) }?.toSet()
      val fresh = mutableListOf<Pair<String, String>>()
      val next = HashSet<String>()
      var connected = 0
      for (i in 0 until devices.length()) {
        val d = devices.getJSONObject(i)
        val mac = d.optString("MAC").lowercase(Locale.ROOT)
        if (mac.isEmpty() || !d.optBoolean("Connected", true)) continue
        // Issues #86/#87: a device the core marked (this phone, "Self"; an
        // owner's phone, "Owner") is not one to watch: no alert, no count.
        if (d.optString("Mark").isNotEmpty()) continue
        connected++
        next += mac
        val arrived = previous != null && mac !in previous
        if (arrived && !d.optBoolean("Approved") && !d.optBoolean("Blocked")) {
          fresh += d.optString("MAC") to displayName(d.optString("CustomName"), d.optString("Hostname"), cfg.unnamedText)
        }
      }
      // First reading for this box: everything there is recorded silently,
      // otherwise turning the alerts on would ring once per existing device.
      WatchConfig.saveKnown(this, next)
      runAutoReject(core)
      for ((mac, name) in fresh) notifyNewDevice(cfg, mac, name)
      if (fresh.isNotEmpty()) CoreHolder.note("info", "veille: ${fresh.size} appareil(s) non approuvé(s) arrivé(s)")
      sampleUsage(core)
      val time = SimpleDateFormat("HH:mm", Locale.FRANCE).format(Date())
      updateOngoing(cfg, cfg.ongoingCountText.replace("{count}", connected.toString()).replace("{time}", time))
    } catch (e: Exception) {
      // Off the home Wi-Fi the box is unreachable: keep trying, quietly.
      CoreHolder.note("warn", "veille: ${e.message}")
      updateOngoing(cfg, cfg.ongoingErrorText)
    }
  }

  // The Conso tab counts only what the phone sampled: one reading of the
  // box's byte counters per tick keeps the month's figures going with the
  // app closed. It never stands in the way of the new-device alerts; a box
  // without counters answers 501, which is not worth a line every tick.
  private fun sampleUsage(core: org.kergui.mobilecore.Core) {
    try {
      val status = CoreHolder.request(core, "POST", "/api/usage/sample", "").status.toInt()
      if (status != 200 && status != 501) CoreHolder.note("warn", "veille: relevé de conso a répondu $status")
    } catch (e: Exception) {
      CoreHolder.note("warn", "veille: relevé de conso: ${e.message}")
    }
  }

  // One round of auto-reject. The core holds the setting and checks the
  // tier: off, or a pass that no longer covers it, and nothing is refused.
  // It also holds the hub's last word on who enforces, which the open app
  // hands it (#36): while the household's relay does, the round answers
  // enforcer=relay and touches nothing. This service never calls the hub.
  // Returns whether the rule is on and enforced. A box without Wi-Fi access
  // control answers 501, which is not worth a line every tick.
  private fun runAutoReject(core: org.kergui.mobilecore.Core): Boolean {
    // This phone is the household's relay and enforces the rule itself
    // (#36, #45): one enforcer at a time.
    if (runCatching { JSONObject(core.relayStatus()).optBoolean("autoReject") }.getOrDefault(false)) return false
    try {
      val res = CoreHolder.request(core, "POST", "/api/autoreject/run", "")
      val status = res.status.toInt()
      if (status != 200) {
        if (status != 501) CoreHolder.note("warn", "veille: rejet automatique a répondu $status")
        return false
      }
      val out = JSONObject(res.body)
      val refused = out.optJSONArray("refused") ?: JSONArray()
      val cfg = WatchConfig.load(this)
      for (i in 0 until refused.length()) {
        val d = refused.getJSONObject(i)
        notifyRefused(cfg, d.optString("mac"), d.optString("name"))
      }
      return out.optBoolean("enabled") && out.optBoolean("entitled")
    } catch (e: Exception) {
      CoreHolder.note("warn", "veille: rejet automatique: ${e.message}")
      return false
    }
  }

  // A notification never names a device by its MAC (#114): the household
  // cannot read one.
  private fun displayName(custom: String, host: String, unnamed: String) =
    custom.ifBlank { host.ifBlank { unnamed } }

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
    val b = Notification.Builder(this, ALERT_CHANNEL)
      .setSmallIcon(iconRes())
      .setContentTitle(cfg.alertTitle)
      .setContentText(cfg.alertBody.replace("{name}", name).replace("{mac}", mac))
      .setAutoCancel(true)
      .setCategory(Notification.CATEGORY_STATUS)
      .setContentIntent(openApp("device/" + Uri.encode(mac)))
    // « Approuver » (#58): it never rings again, and auto-reject spares it.
    val approve = Intent(this, ApproveReceiver::class.java)
      .setAction(ApproveReceiver.ACTION)
      .putExtra(ApproveReceiver.EXTRA_MAC, mac)
    val pi = PendingIntent.getBroadcast(this, mac.hashCode(), approve, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    b.addAction(Notification.Action.Builder(null, cfg.approveText, pi).build())
    getSystemService(NotificationManager::class.java).notify(mac.hashCode(), b.build())
  }

  // A device auto-reject refused: tapping it opens its sheet, where its
  // access can be given back for free.
  private fun notifyRefused(cfg: WatchConfig, mac: String, name: String) {
    if (mac.isEmpty()) return
    val n = Notification.Builder(this, ALERT_CHANNEL)
      .setSmallIcon(iconRes())
      .setContentTitle(cfg.refusedTitle)
      .setContentText(cfg.refusedBody.replace("{name}", name.ifBlank { cfg.unnamedText }).replace("{mac}", mac))
      .setAutoCancel(true)
      .setCategory(Notification.CATEGORY_STATUS)
      .setContentIntent(openApp("device/" + Uri.encode(mac)))
      .build()
    // Same id as its new-device notification, which it replaces.
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
