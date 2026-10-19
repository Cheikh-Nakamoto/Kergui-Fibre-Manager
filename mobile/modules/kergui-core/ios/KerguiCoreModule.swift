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
  }
}
