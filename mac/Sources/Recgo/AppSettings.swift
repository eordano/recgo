import Foundation
import SwiftUI
import ServiceManagement

enum RecordMode: String, CaseIterable, Identifiable {
    case screen, browser, tab, audio
    var id: String { rawValue }

    var label: String {
        switch self {
        case .screen: return "Screen"
        case .browser: return "Browser"
        case .tab: return "This Tab"
        case .audio: return "Audio only"
        }
    }

    var shortcutKey: String {
        switch self {
        case .screen: return "shortcutRecordScreen"
        case .browser: return "shortcutRecordBrowser"
        case .tab: return "shortcutRecordTab"
        case .audio: return "shortcutRecordAudio"
        }
    }

    // Empty when no chord is configured for the mode.
    var hotkeyLabel: String { Hotkeys.label(forKey: shortcutKey) }

    var binary: String {
        switch self {
        // Audio only is the desktop recorder with -no-video: the same
        // session pipeline (marks, live doc, SESSION.md), minus the screen.
        // The recgo TUI is terminal-only and speaks none of that protocol.
        case .screen, .audio: return "recgo-desktop"
        case .browser: return "recgo-browser"
        case .tab: return "recgo-tab"
        }
    }
}

// Every knob the Settings window exposes, persisted in UserDefaults and
// translated into recorder flags by Recorder.buildArguments.
final class AppSettings: ObservableObject {
    static let shared = AppSettings()

    @AppStorage("outRoot") var outRoot =
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Documents/walk-and-talk").path
    @AppStorage("menuTimer") var menuTimer = true
    @AppStorage("autoTitle") var autoTitle = true
    @AppStorage("defaultMode") var defaultModeRaw = RecordMode.screen.rawValue
    @AppStorage("clickShots") var clickShots = true
    @AppStorage("focusShots") var focusShots = true
    @AppStorage("captureMic") var captureMic = true
    @AppStorage("micDevice") var micDevice = ""
    @AppStorage("sttBackend") var sttBackend = "auto"
    @AppStorage("sttLanguage") var sttLanguage = ""
    @AppStorage("whisperModel") var whisperModel = ""
    @AppStorage("whisperBin") var whisperBin = ""
    @AppStorage("liveNarrationHUD") var liveNarrationHUD = true
    @AppStorage("neverUpload") var neverUpload = true
    @AppStorage("syncEnabled") var syncEnabled = false
    @AppStorage("syncTarget") var syncTarget = ""
    @AppStorage("syncKey") var syncKey = ""
    @AppStorage("portalEnabled") var portalEnabled = false
    @AppStorage("portalURL") var portalURL = ""
    @AppStorage("portalRoom") var portalRoom = ""
    @AppStorage("binDir") var binDir = ""
    // A second library source: a browsed folder, e.g. a mounted volume
    // holding a remote host's sessions.
    @AppStorage("extraRoot") var extraRoot = ""

    // Global hotkey chords, e.g. "cmd+shift+1". Empty means the action has
    // no system-wide key; nothing is grabbed out of the box.
    @AppStorage("shortcutRecordScreen") var shortcutRecordScreen = ""
    @AppStorage("shortcutRecordBrowser") var shortcutRecordBrowser = ""
    @AppStorage("shortcutRecordTab") var shortcutRecordTab = ""
    @AppStorage("shortcutRecordAudio") var shortcutRecordAudio = ""
    @AppStorage("shortcutMark") var shortcutMark = ""
    @AppStorage("shortcutToggleHUD") var shortcutToggleHUD = ""
    @AppStorage("shortcutStop") var shortcutStop = ""
    @AppStorage("shortcutOpenLibrary") var shortcutOpenLibrary = ""

    var defaultMode: RecordMode {
        get { RecordMode(rawValue: defaultModeRaw) ?? .screen }
        set { defaultModeRaw = newValue.rawValue }
    }

    var outRootURL: URL { URL(fileURLWithPath: (outRoot as NSString).expandingTildeInPath) }

    // Portal and sync are hard-gated by the one privacy switch, same as the
    // demo: when neverUpload is on they do not merely hide, they cannot fire.
    var portalActive: Bool { portalEnabled && !neverUpload && !portalURL.isEmpty }
    var syncActive: Bool { syncEnabled && !neverUpload && !syncTarget.isEmpty }

    // The rsync-able location a finished session lands at, for handing to an
    // agent or a teammate on another host.
    func remoteLocation(for sessionID: String) -> String? {
        guard syncActive else { return nil }
        return syncTarget.hasSuffix("/") ? syncTarget + sessionID
            : syncTarget + "/" + sessionID
    }
    var effectiveSTTBackend: String {
        if neverUpload && (sttBackend == "auto" || sttBackend == "remote") { return "local" }
        return sttBackend
    }

    var launchAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue { try SMAppService.mainApp.register() }
                else { try SMAppService.mainApp.unregister() }
            } catch {
                NSLog("launch at login: \(error)")
            }
            objectWillChange.send()
        }
    }

    // Where the Go recorders live: an explicit override, the app bundle's
    // Helpers directory, then PATH (including the usual local-bin spots).
    func resolveBinary(_ name: String) -> URL? {
        let fm = FileManager.default
        var candidates: [URL] = []
        if !binDir.isEmpty {
            candidates.append(URL(fileURLWithPath: (binDir as NSString).expandingTildeInPath)
                .appendingPathComponent(name))
        }
        candidates.append(Bundle.main.bundleURL
            .appendingPathComponent("Contents/Helpers").appendingPathComponent(name))
        let home = fm.homeDirectoryForCurrentUser.path
        var dirs = (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":")
            .map(String.init)
        dirs += ["\(home)/.local/bin", "/opt/homebrew/bin", "/usr/local/bin",
                 "/run/current-system/sw/bin"]
        candidates += dirs.map { URL(fileURLWithPath: $0).appendingPathComponent(name) }
        return candidates.first { fm.isExecutableFile(atPath: $0.path) }
    }
}
