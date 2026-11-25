import ExpoModulesCore
import KerguiCore
import os

final class CoreNotOpenException: Exception {
  override var reason: String { "Kergui core is not open: call open() first" }
}

final class CoreException: GenericException<String> {
  override var reason: String { param }
}

private final class CoreLogger: NSObject, MobilecoreLoggerProtocol {
  private let osLog = os.Logger(subsystem: "org.kergui", category: "core")

  func log(_ level: String?, message: String?) {
    let msg = message ?? ""
    switch level {
    case "error": osLog.error("\(msg, privacy: .public)")
    case "warn": osLog.warning("\(msg, privacy: .public)")
    case "info": osLog.info("\(msg, privacy: .public)")
    default: osLog.debug("\(msg, privacy: .public)")
    }
  }
}

// Bridges the gomobile-generated Go core to JS. All router I/O happens in Go;
// AsyncFunctions run off the main thread.
public final class KerguiCoreModule: Module {
  // One core per process: it owns the SQLite file and the single router session.
  private static var shared: MobilecoreCore?
  private static let lock = NSLock()
  private static let logger = CoreLogger()

  private func core() throws -> MobilecoreCore {
    KerguiCoreModule.lock.lock()
    defer { KerguiCoreModule.lock.unlock() }
    guard let c = KerguiCoreModule.shared else { throw CoreNotOpenException() }
    return c
  }

  private func wrap<T>(_ block: () throws -> T) throws -> T {
    do {
      return try block()
    } catch let e as Exception {
      throw e
    } catch {
      throw CoreException(error.localizedDescription)
    }
  }

  public func definition() -> ModuleDefinition {
    Name("KerguiCore")

    Function("version") { MobilecoreVersion() }

    AsyncFunction("open") { (vaultKey: String) in
      KerguiCoreModule.lock.lock()
      defer { KerguiCoreModule.lock.unlock() }
      if KerguiCoreModule.shared != nil { return }
      let base = try FileManager.default.url(
        for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
      var dir = base.appendingPathComponent("kergui", isDirectory: true)
      try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
      var values = URLResourceValues()
      values.isExcludedFromBackup = true  // credentials stay on this device
      try? dir.setResourceValues(values)
      var err: NSError?
      let c = MobilecoreOpen(dir.path, vaultKey, KerguiCoreModule.logger, &err)
      if let err { throw CoreException(err.localizedDescription) }
      KerguiCoreModule.shared = c
    }

    AsyncFunction("configure") { (routerUrl: String, adapterId: String, pinSha256: String) in
      let c = try self.core()
      try self.wrap { try c.configure(routerUrl, adapterID: adapterId, pinSHA256: pinSha256) }
    }

    AsyncFunction("request") { (method: String, path: String, body: String) -> [String: Any] in
      let c = try self.core()
      let res = try self.wrap { try c.request(method, path: path, body: body) }
      return ["status": Int(res.status), "body": res.body]
    }

    // The paid-action gate inside the core (RFC-006): the hub keys it trusts
    // and the current entitlement token ("" for none).
    AsyncFunction("setPublicKeys") { (keys: String) in
      let c = try self.core()
      try self.wrap { try c.setPublicKeys(keys) }
    }

    AsyncFunction("setEntitlement") { (token: String) in
      let c = try self.core()
      c.setEntitlement(token)
    }

    // The owner's keys (issue #43). They are generated and kept in the Go
    // core's AES-GCM vault; only public keys, the QR payload, the six digits
    // and signed bundles cross this bridge.
    AsyncFunction("ownerKeys") { () -> String in
      let c = try self.core()
      return try self.wrap { try c.ownerKeys() }
    }

    AsyncFunction("pairingInvite") { (code: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.pairingInvite(code) }
    }

    AsyncFunction("pairingCode") { (relayEd25519: String, relayX25519: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.pairingCode(relayEd25519, relayX25519: relayX25519) }
    }

    AsyncFunction("signTrustBundle") { (tenantId: String, current: String, change: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.signTrustBundle(tenantId, current: current, change: change) }
    }

    // The relay policy (#36): the auto-reject rule and the entitlement
    // token, signed by the owner from the core's own state; "" when there
    // is nothing to send.
    AsyncFunction("signRelayPolicy") { (tenantId: String, bundle: String, current: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.signRelayPolicy(tenantId, bundle: bundle, current: current) }
    }

    AsyncFunction("sealCredentials") { (tenantId: String, bundle: String, agentId: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.sealCredentials(tenantId, bundle: bundle, agentID: agentId) }
    }

    // Remote mode (issue #46): the core opens what the hub serves against
    // the owner's bundle and signs the commands; JS does the HTTP only.
    AsyncFunction("remoteRelays") { (tenantId: String, bundle: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.remoteRelays(tenantId, bundle: bundle) }
    }

    AsyncFunction("openDeviceSnapshot") { (tenantId: String, bundle: String, snapshot: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.openDeviceSnapshot(tenantId, bundle: bundle, snapshot: snapshot) }
    }

    AsyncFunction("signDeviceCommand") { (tenantId: String, mac: String, action: String, boxId: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.signDeviceCommand(tenantId, mac: mac, action: action, boxID: boxId) }
    }

    // Consumption, SSIDs, Wi-Fi access and cut / restore Internet through
    // the relay (issue #50): any request of the relay's allow-list.
    AsyncFunction("signRemoteRequest") { (tenantId: String, bundle: String, method: String, path: String, body: String, boxId: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.signRemoteRequest(tenantId, bundle: bundle, method: method, path: path, body: body, boxID: boxId) }
    }

    AsyncFunction("verifyCommandResult") { (tenantId: String, cmdId: String, bundle: String, result: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.verifyCommandResult(tenantId, cmdID: cmdId, bundle: bundle, result: result) }
    }

    // Member delegations (issue #47): this phone's member key, kept in the
    // core's vault; the member's commands are signed with it, and the
    // owner's core renews the members' delegations.
    AsyncFunction("memberKeys") { () -> String in
      let c = try self.core()
      return try self.wrap { try c.memberKeys() }
    }

    AsyncFunction("signMemberCommand") { (tenantId: String, bundle: String, mac: String, action: String, boxId: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.signMemberCommand(tenantId, bundle: bundle, mac: mac, action: action, boxID: boxId) }
    }

    AsyncFunction("renewDelegations") { (tenantId: String, current: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.renewDelegations(tenantId, current: current) }
    }

    // Recovery of a lost owner phone (issue #48): a co-owner's phone takes
    // the household over, or a new phone proves the box password to the
    // relay on the home Wi-Fi. The core signs; JS uploads the bundle.
    AsyncFunction("takeOverHousehold") { (tenantId: String, bundle: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.takeOverHousehold(tenantId, bundle: bundle) }
    }

    AsyncFunction("recoverAtHome") { (relayAddress: String, boxPassword: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.recoverAtHome(relayAddress, boxPassword: boxPassword) }
    }

    // « Mot de passe de la box » in Réglages (#93): the app asks only after
    // the phone's own unlock. A core function, never an API route.
    AsyncFunction("boxPassword") { () -> String in
      let c = try self.core()
      return try self.wrap { try c.boxPassword() }
    }

    AsyncFunction("demoRelayRedeem") { (invite: String) -> String in
      let c = try self.core()
      return try self.wrap { try c.demoRelayRedeem(invite) }
    }

    AsyncFunction("certificateFingerprint") { (routerUrl: String) -> String in
      var err: NSError?
      let fp = MobilecoreCertificateFingerprint(routerUrl, &err)
      if let err { throw CoreException(err.localizedDescription) }
      return fp
    }

    // The journal screen. It reads the core's ring buffer rather than
    // os_log: a household debugging a connection cannot open Console.app.
    AsyncFunction("journal") { (since: Int, limit: Int) -> String in
      let c = try self.core()
      var out: String = "[]"
      try self.wrap { out = try c.journal(Int64(since), limit: limit) }
      return out
    }

    // One line from the app into the same journal. A core that is not open
    // yet has nowhere to keep it, so it only reaches os_log.
    Function("note") { (level: String, message: String) in
      KerguiCoreModule.lock.lock()
      let c = KerguiCoreModule.shared
      KerguiCoreModule.lock.unlock()
      if let c { c.note(level, message: message) } else {
        KerguiCoreModule.logger.log(level, message: message)
      }
    }

    AsyncFunction("startDemo") {
      let c = try self.core()
      try self.wrap { try c.startDemo() }
    }

    AsyncFunction("stopDemo") {
      try? self.core().stopDemo()
    }

    // Self-update (#74) is the Android website APK's only: the App Store
    // updates the iPhone app. Not supported here.
    Function("selfUpdateInfo") { () -> [String: Any] in
      return ["allowed": false, "installer": "", "build": 0]
    }

    Function("onUnmeteredNetwork") { () -> Bool in
      return false
    }

    Function("canInstallPackages") { () -> Bool in
      return false
    }
  }
}
