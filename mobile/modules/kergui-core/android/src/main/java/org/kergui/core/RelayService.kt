package org.kergui.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.PowerManager
import org.json.JSONObject
import org.kergui.mobilecore.RelayNetwork
import java.net.Inet4Address
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

// Makes this phone the household's relay (issue #45, RFC-005): the Go core
// runs internal/agent's loop (heartbeat every 20 s, lease, long poll of the
// owner's signed commands, signed results and snapshots); this service keeps
// the process alive with the app closed, follows the home Wi-Fi, binds the
// process and every Go socket to it, and shows what the relay is doing in
// its ongoing notification, with an "Arrêter" action.
//
// Type connectedDevice, not dataSync: dataSync is capped at 6 h a day from
// Android 15, and the relay is the link to a device on the local network
// (the box). Its prerequisite is CHANGE_NETWORK_STATE, which the manifest
// declares.
//
// One box session: the watch service (WatchService) runs in the same
// process on the same core, whose calls to the box all take one lock and
// share one session, so the two never log each other out. While this relay
// enforces auto-reject (the core says so), the watch service skips its own
// round of it.
class RelayService : Service() {
  private var executor: ScheduledExecutorService? = null
  private var wakeLock: PowerManager.WakeLock? = null
  private var callback: ConnectivityManager.NetworkCallback? = null

  @Volatile private var wifi: Network? = null
  @Volatile private var wifiInfo: WifiInfo? = null
  @Volatile private var link: LinkProperties? = null

  override fun onBind(intent: Intent?): IBinder? = null

  override fun onCreate() {
    super.onCreate()
    val cfg = RelayConfig.load(this)
    ensureChannel(this, cfg)
    val notif = ongoing(cfg, cfg.startingText)
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
      startForeground(ONGOING_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
    } else {
      startForeground(ONGOING_ID, notif)
    }
    // The heartbeat must land every 20 s with the screen off, or the lease
    // (60 s) lapses. The work is a few small HTTPS exchanges per beat.
    wakeLock = (getSystemService(Context.POWER_SERVICE) as PowerManager)
      .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "kergui:relay")
      .apply { setReferenceCounted(false); acquire() }
    watchWifi()
  }

  override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
    val cfg = RelayConfig.load(this)
    if (intent?.action == ACTION_STOP) {
      // "Arrêter" on the notification: off until turned on again.
      RelayConfig.disable(this)
      stopSelf()
      return START_NOT_STICKY
    }
    if (!cfg.enabled || cfg.vaultKey.isEmpty()) {
      stopSelf()
      return START_NOT_STICKY
    }
    executor?.shutdownNow()
    executor = Executors.newSingleThreadScheduledExecutor().also {
      it.scheduleWithFixedDelay({ tick() }, 0, TICK_SEC, TimeUnit.SECONDS)
    }
    CoreHolder.note("info", "relais: service démarré")
    return START_STICKY
  }

  override fun onDestroy() {
    executor?.shutdownNow()
    executor = null
    // Stands down cleanly: the lease goes back to the hub at once.
    Thread { CoreHolder.current()?.relayStop() }.apply { start(); join(STOP_WAIT_MS) }
    callback?.let { runCatching { connectivity().unregisterNetworkCallback(it) } }
    callback = null
    connectivity().bindProcessToNetwork(null)
    wakeLock?.let { if (it.isHeld) it.release() }
    CoreHolder.note("info", "relais: service arrêté")
    super.onDestroy()
  }

  private fun connectivity() = getSystemService(ConnectivityManager::class.java)

  // Follows the Wi-Fi: the process is bound to it, so nothing falls back to
  // mobile data, and its name is read for the Wi-Fi signature. Reading the
  // SSID needs the location permission; without it Android answers
  // "<unknown ssid>" and the core checks the box identity alone.
  private fun watchWifi() {
    val request = NetworkRequest.Builder().addTransportType(NetworkCapabilities.TRANSPORT_WIFI).build()
    val cb = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
      object : ConnectivityManager.NetworkCallback(FLAG_INCLUDE_LOCATION_INFO) {
        override fun onAvailable(network: Network) = onWifi(network)
        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
          wifiInfo = caps.transportInfo as? WifiInfo
        }
        override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) { link = lp }
        override fun onLost(network: Network) = onWifiLost(network)
      }
    } else {
      object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) = onWifi(network)
        override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) { link = lp }
        override fun onLost(network: Network) = onWifiLost(network)
      }
    }
    callback = cb
    connectivity().registerNetworkCallback(request, cb)
  }

  private fun onWifi(network: Network) {
    wifi = network
    link = connectivity().getLinkProperties(network)
    connectivity().bindProcessToNetwork(network)
  }

  private fun onWifiLost(network: Network) {
    if (wifi != network) return
    wifi = null
    wifiInfo = null
    connectivity().bindProcessToNetwork(null)
  }

  @Suppress("DEPRECATION")
  private fun currentWifiInfo(): WifiInfo? =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) wifiInfo
    else applicationContext.getSystemService(WifiManager::class.java)?.connectionInfo

  // What the Go core asks of the phone's network (mobilecore.RelayNetwork).
  private val network = object : RelayNetwork {
    override fun ssid(): String {
      val raw = currentWifiInfo()?.ssid ?: return ""
      if (raw == WifiManager.UNKNOWN_SSID || raw.isBlank()) return ""
      return raw.removeSurrounding("\"")
    }

    override fun bssid(): String {
      val b = currentWifiInfo()?.bssid ?: return ""
      // Android's placeholder when the location permission is missing.
      return if (b == "02:00:00:00:00:00") "" else b
    }

    override fun lanip(): String =
      link?.linkAddresses?.map { it.address }?.firstOrNull { it is Inet4Address }?.hostAddress ?: ""

    override fun bindSocket(fd: Long): Boolean {
      val n = wifi ?: return false
      return try {
        // fromFd duplicates the descriptor: the binding is on the socket,
        // which both share, and closing the copy leaves Go's open.
        ParcelFileDescriptor.fromFd(fd.toInt()).use { n.bindSocket(it.fileDescriptor) }
        true
      } catch (e: Exception) {
        false
      }
    }
  }

  // The box's address: the Wi-Fi's gateway first, then the usual ones.
  private fun candidates(): String {
    val gw = link?.routes?.firstOrNull { it.isDefaultRoute && it.gateway is Inet4Address }?.gateway?.hostAddress
    return (listOfNotNull(gw?.let { "https://$it" }) + FALLBACK_BOXES).distinct().joinToString(",")
  }

  private fun tick() {
    val cfg = RelayConfig.load(this)
    try {
      val core = CoreHolder.open(this, cfg.vaultKey)
      if (!core.relayRunning()) {
        if (wifi == null) {
          updateOngoing(cfg, cfg.noWifiText)
          return
        }
        try {
          CoreHolder.findBoxForRelay(core, candidates())
        } catch (e: Exception) {
          CoreHolder.note("warn", "relais: ${e.message}")
          updateOngoing(cfg, cfg.noBoxText)
          return
        }
        core.relayStart(network)
      }
      val st = JSONObject(core.relayStatus())
      val text = when (st.optString("state")) {
        "active" -> if (st.optString("check") == "box-identity") cfg.activeBoxOnlyText else cfg.activeText
        "standby" -> cfg.standbyText
        // The owner's phone holds the box at home (#94): the relay keeps
        // its link with the hub and leaves the box alone.
        "paused" -> cfg.pausedText
        "wrong-network" -> cfg.wrongNetworkText
        "offline" -> cfg.offlineText
        else -> cfg.startingText
      }
      updateOngoing(cfg, text)
    } catch (e: Exception) {
      CoreHolder.note("warn", "relais: ${e.message}")
      updateOngoing(cfg, cfg.offlineText)
    }
  }

  private fun ongoing(cfg: RelayConfig, text: String): Notification {
    val open = packageManager.getLaunchIntentForPackage(packageName)?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) ?: Intent()
    val stop = PendingIntent.getService(
      this, 1, Intent(this, RelayService::class.java).setAction(ACTION_STOP),
      PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )
    return Notification.Builder(this, CHANNEL)
      .setSmallIcon(iconRes())
      .setContentTitle(cfg.title)
      .setContentText(text)
      .setStyle(Notification.BigTextStyle().bigText(text))
      .setOngoing(true)
      .setOnlyAlertOnce(true)
      .setContentIntent(PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
      .addAction(Notification.Action.Builder(null, cfg.stopText, stop).build())
      .build()
  }

  private fun updateOngoing(cfg: RelayConfig, text: String) {
    getSystemService(NotificationManager::class.java).notify(ONGOING_ID, ongoing(cfg, text))
  }

  private fun iconRes(): Int {
    val id = resources.getIdentifier("notification_icon", "drawable", packageName)
    return if (id != 0) id else applicationInfo.icon
  }

  companion object {
    const val ONGOING_ID = 4202
    const val CHANNEL = "relay"
    const val ACTION_STOP = "org.kergui.core.RELAY_STOP"
    // The core paces the relay itself; this only refreshes the
    // notification and restarts the loop once the Wi-Fi and the box are back.
    private const val TICK_SEC = 15L
    private const val STOP_WAIT_MS = 5000L
    private val FALLBACK_BOXES = listOf("https://192.168.1.1", "https://192.168.0.1", "https://192.168.1.254", "https://192.168.100.1")

    fun ensureChannel(ctx: Context, cfg: RelayConfig) {
      ctx.getSystemService(NotificationManager::class.java)
        .createNotificationChannel(NotificationChannel(CHANNEL, cfg.channel, NotificationManager.IMPORTANCE_LOW))
    }

    fun start(ctx: Context) {
      val i = Intent(ctx, RelayService::class.java)
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) ctx.startForegroundService(i) else ctx.startService(i)
    }

    fun stop(ctx: Context) {
      ctx.stopService(Intent(ctx, RelayService::class.java))
    }
  }
}
