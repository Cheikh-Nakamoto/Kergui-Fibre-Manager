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

    // The owner's keys (issue #43). They are generated and kept in the Go
    // core's AES-GCM vault; only public keys, the QR payload, the six digits
    // and signed bundles cross this bridge.
    AsyncFunction("ownerKeys") {
      wrap { core().ownerKeys() }
    }

    AsyncFunction("pairingInvite") { code: String ->
      wrap { core().pairingInvite(code) }
    }

    AsyncFunction("pairingCode") { relayEd25519: String, relayX25519: String ->
      wrap { core().pairingCode(relayEd25519, relayX25519) }
    }

    AsyncFunction("signTrustBundle") { tenantId: String, current: String, change: String ->
      wrap { core().signTrustBundle(tenantId, current, change) }
    }

    AsyncFunction("sealCredentials") { tenantId: String, bundle: String, agentId: String ->
      wrap { core().sealCredentials(tenantId, bundle, agentId) }
    }

    // Remote mode (issue #46): the core opens what the hub serves against
    // the owner's bundle and signs the commands; JS does the HTTP only.
    AsyncFunction("remoteRelays") { tenantId: String, bundle: String ->
      wrap { core().remoteRelays(tenantId, bundle) }
    }

    AsyncFunction("openDeviceSnapshot") { tenantId: String, bundle: String, snapshot: String ->
      wrap { core().openDeviceSnapshot(tenantId, bundle, snapshot) }
    }

    AsyncFunction("signDeviceCommand") { tenantId: String, mac: String, action: String, boxId: String ->
      wrap { core().signDeviceCommand(tenantId, mac, action, boxId) }
    }

    // Consumption, SSIDs, Wi-Fi access and cut / restore Internet through
    // the relay (issue #50): any request of the relay's allow-list.
    AsyncFunction("signRemoteRequest") { tenantId: String, bundle: String, method: String, path: String, body: String, boxId: String ->
      wrap { core().signRemoteRequest(tenantId, bundle, method, path, body, boxId) }
    }

    AsyncFunction("verifyCommandResult") { tenantId: String, cmdId: String, bundle: String, result: String ->
      wrap { core().verifyCommandResult(tenantId, cmdId, bundle, result) }
    }

    // Member delegations (issue #47): this phone's member key, kept in the
    // core's vault; the member's commands are signed with it, and the
    // owner's core renews the members' delegations.
    AsyncFunction("memberKeys") {
      wrap { core().memberKeys() }
    }

    AsyncFunction("signMemberCommand") { tenantId: String, bundle: String, mac: String, action: String, boxId: String ->
      wrap { core().signMemberCommand(tenantId, bundle, mac, action, boxId) }
    }

    AsyncFunction("renewDelegations") { tenantId: String, current: String ->
      wrap { core().renewDelegations(tenantId, current) }
    }

    // Recovery of a lost owner phone (issue #48): a co-owner's phone takes
    // the household over, or a new phone proves the box password to the
    // relay on the home Wi-Fi. The core signs; JS uploads the bundle.
    AsyncFunction("takeOverHousehold") { tenantId: String, bundle: String ->
      wrap { core().takeOverHousehold(tenantId, bundle) }
    }

    AsyncFunction("recoverAtHome") { relayAddress: String, boxPassword: String ->
      wrap { core().recoverAtHome(relayAddress, boxPassword) }
    }

    AsyncFunction("demoRelayRedeem") { invite: String ->
      wrap { core().demoRelayRedeem(invite) }
    }

    // This phone as the household's relay (issues #43, #45). Its keys, hub
    // token and the sealed box password stay in the core's vault.
    AsyncFunction("relayEnroll") { hubUrl: String, invite: String, name: String ->
      wrap { core().relayEnroll(hubUrl, invite, name) }
    }

    AsyncFunction("relayEnrolled") {
      wrap { core().relayEnrolled() }
    }

    AsyncFunction("relayCheckTrust") {
      wrap { core().relayCheckTrust() }
    }

    AsyncFunction("relayStatus") {
      CoreHolder.current()?.relayStatus() ?: "{\"running\":false,\"state\":\"stopped\"}"
    }

    // RelayService. `config` carries the vault key and the translated
    // texts of its notification.
    AsyncFunction("startRelay") { config: Map<String, Any?> ->
      val ctx = context()
      RelayConfig.save(ctx, config)
      RelayService.ensureChannel(ctx, RelayConfig.load(ctx))
      RelayService.start(ctx)
    }

    AsyncFunction("stopRelay") {
      val ctx = context()
      RelayConfig.disable(ctx)
      RelayService.stop(ctx)
    }

    Function("relayEnabled") {
      appContext.reactContext?.let { RelayConfig.load(it).enabled } ?: false
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
