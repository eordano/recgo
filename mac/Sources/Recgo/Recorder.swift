import Foundation
import AppKit

enum RecorderPhase: Equatable {
    case idle
    case recording
    case finishing
}

// Drives one Go recorder process. The whole control surface is the one the
// CLIs already expose: flags at spawn, "m" on stdin for a mark, SIGINT to
// stop, SESSION.live.md for the live document, and a final
// "wrote <dir>/SESSION.md" line on stderr naming the packed session.
final class Recorder: ObservableObject {
    static let shared = Recorder()

    @Published var phase: RecorderPhase = .idle
    @Published var mode: RecordMode = .screen
    @Published var elapsed: TimeInterval = 0
    // Stop is not instant: the recorder packs (transcription, title, sync)
    // between SIGINT and exit. These drive the honest "Finishing" UI:
    // how long the pack has run, and the recorder's own last stderr line.
    @Published var finishingElapsed: TimeInterval = 0
    @Published var finishingStatus = ""
    @Published var liveDoc = SessionDoc()
    @Published var lastNarration = ""
    @Published var marks = 0
    @Published var lastError = ""
    @Published var finishedSessionDir: URL?
    // The portal room this session actually joined. recgo requires an
    // explicit room (the name is the only credential), so when the setting
    // is blank the app mints an unguessable one per session.
    @Published var activeRoom = ""

    var onFinished: ((URL?) -> Void)?
    var onStarted: (() -> Void)?

    private var process: Process?
    private var stdinPipe: Pipe?
    private var stderrTail: [String] = []
    private var startDate: Date?
    private var stopDate: Date?
    private var tick: Timer?
    private var liveDirURL: URL?

    var isRecording: Bool { phase == .recording }
    var isBusy: Bool { phase != .idle }

    func start(_ mode: RecordMode) {
        guard phase == .idle else { return }
        self.mode = mode
        lastError = ""
        finishedSessionDir = nil
        spawn(mode)
    }

    private func buildArguments(_ mode: RecordMode) -> [String] {
        let s = AppSettings.shared
        var args: [String] = ["-out", s.outRootURL.path]
        if mode == .audio {
            // The mic is the whole recording here; the capture-mic toggle
            // only applies to the modes where it is an add-on.
            args.append("-no-video")
        } else if !s.captureMic {
            args.append("-no-audio")
        }
        if !s.micDevice.isEmpty { args += ["-mic", s.micDevice] }
        args += ["-stt-backend", s.effectiveSTTBackend]
        if !s.sttLanguage.isEmpty { args += ["-stt-language", s.sttLanguage] }
        if !s.whisperModel.isEmpty { args += ["-whisper-model", s.whisperModel] }
        if !s.whisperBin.isEmpty { args += ["-whisper-bin", s.whisperBin] }
        if !s.autoTitle { args += ["-title-backend", "none"] }
        if mode == .screen {
            args.append("-click-shots=\(s.clickShots)")
            args.append("-focus-shots=\(s.focusShots)")
        }
        if s.portalActive {
            activeRoom = s.portalRoom.isEmpty ? Recorder.mintRoom() : s.portalRoom
            args += ["-portal", s.portalURL, "-portal-room", activeRoom]
        } else {
            activeRoom = ""
        }
        if s.syncActive {
            args += ["-sync-target", s.syncTarget]
            if !s.syncKey.isEmpty {
                args += ["-sync-key", (s.syncKey as NSString).expandingTildeInPath]
            }
        } else {
            args.append("-no-sync")
        }
        return args
    }

    static func mintRoom() -> String {
        let a = ["walk", "amber", "quiet", "brisk", "misty", "solar", "cedar", "ember"]
        let b = ["lantern", "harbor", "meadow", "signal", "orbit", "thicket",
                 "compass", "quarry"]
        let suffix = (0..<4).map { _ in
            "23456789abcdefghjkmnpqrstuvwxyz".randomElement()!
        }
        return "\(a.randomElement()!)-\(b.randomElement()!)-\(String(suffix))"
    }

    private func spawn(_ mode: RecordMode) {
        guard let bin = AppSettings.shared.resolveBinary(mode.binary) else {
            phase = .idle
            lastError = "\(mode.binary) not found — set the binaries folder in Settings"
            onFinished?(nil)
            return
        }

        let p = Process()
        p.executableURL = bin
        p.arguments = buildArguments(mode)
        p.currentDirectoryURL = FileManager.default.homeDirectoryForCurrentUser

        // A login-launched app gets a bare PATH; the recorders shell out to
        // ffmpeg, whisper-cli and screencapture, so hand them the usual spots.
        var env = ProcessInfo.processInfo.environment
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let extras = ["\(home)/.local/bin", "/opt/homebrew/bin", "/usr/local/bin",
                      "/run/current-system/sw/bin", "/etc/profiles/per-user/\(NSUserName())/bin"]
        var path = (env["PATH"] ?? "/usr/bin:/bin").split(separator: ":").map(String.init)
        for extra in extras where !path.contains(extra) { path.append(extra) }
        env["PATH"] = path.joined(separator: ":")
        p.environment = env

        let stdin = Pipe()
        let stderr = Pipe()
        p.standardInput = stdin
        p.standardError = stderr
        p.standardOutput = Pipe()

        stderrTail = []
        stderr.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            DispatchQueue.main.async {
                self?.consumeStderr(text)
            }
        }
        p.terminationHandler = { [weak self] proc in
            DispatchQueue.main.async {
                stderr.fileHandleForReading.readabilityHandler = nil
                self?.processEnded(status: proc.terminationStatus)
            }
        }

        do {
            try p.run()
        } catch {
            phase = .idle
            lastError = "could not start \(mode.binary): \(error.localizedDescription)"
            onFinished?(nil)
            return
        }

        process = p
        stdinPipe = stdin
        startDate = Date()
        elapsed = 0
        marks = 0
        liveDoc = SessionDoc()
        lastNarration = ""
        liveDirURL = nil
        phase = .recording
        onStarted?()

        tick?.invalidate()
        tick = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            self?.pulse()
        }
    }

    private func pulse() {
        guard let start = startDate else { return }
        if phase == .finishing {
            // The take is over: freeze the session clock and count the pack
            // instead, so the HUD never looks like it is still capturing.
            if let stop = stopDate {
                finishingElapsed = Date().timeIntervalSince(stop)
            }
            return
        }
        guard phase == .recording else { return }
        elapsed = Date().timeIntervalSince(start)
        if liveDirURL == nil, let pid = process?.processIdentifier {
            let root = AppSettings.shared.outRootURL
            let suffix = "-recording-\(pid)"
            if let names = try? FileManager.default.contentsOfDirectory(atPath: root.path),
               let hit = names.first(where: { $0.hasSuffix(suffix) }) {
                liveDirURL = root.appendingPathComponent(hit)
            }
        }
        if let dir = liveDirURL,
           let text = try? String(
               contentsOf: dir.appendingPathComponent("SESSION.live.md"), encoding: .utf8) {
            let doc = parseSessionDoc(text)
            liveDoc = doc
            if let nar = doc.events.last(where: { $0.kind == .narration }) {
                lastNarration = nar.text
            }
        }
    }

    private func consumeStderr(_ text: String) {
        for line in text.split(separator: "\n") {
            stderrTail.append(String(line))
        }
        if stderrTail.count > 60 { stderrTail.removeFirst(stderrTail.count - 60) }
        if phase == .finishing,
           let latest = stderrTail.last(where: {
               !$0.trimmingCharacters(in: .whitespaces).isEmpty
           }) {
            finishingStatus = latest.trimmingCharacters(in: .whitespaces)
        }
    }

    func mark() {
        guard phase == .recording, let pipe = stdinPipe else { return }
        pipe.fileHandleForWriting.write(Data("m\n".utf8))
        marks += 1
    }

    func stop() {
        guard phase == .recording, let p = process else { return }
        stopDate = Date()
        finishingElapsed = 0
        finishingStatus = "stopping..."
        phase = .finishing
        p.interrupt()
    }

    // The recorder restores default signal handling once it starts packing,
    // so a second SIGINT already hard-kills it; SIGKILL after a beat covers
    // a recorder wedged badly enough to mask even that. The packed session
    // is lost but the raw capture stays in the -recording-<pid> folder.
    func forceStop() {
        guard phase == .finishing, let p = process else { return }
        finishingStatus = "force stopping..."
        p.interrupt()
        let pid = p.processIdentifier
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self, self.process === p, self.phase == .finishing else { return }
            kill(pid, SIGKILL)
        }
    }

    private func processEnded(status: Int32) {
        tick?.invalidate()
        tick = nil
        let wrote = stderrTail.last { $0.hasPrefix("wrote ") && $0.hasSuffix("/SESSION.md") }
        var dir: URL?
        if let wrote {
            let path = String(wrote.dropFirst(6).dropLast("/SESSION.md".count))
            dir = URL(fileURLWithPath: path)
        }
        if dir == nil && status != 0 {
            lastError = stderrTail.suffix(4).joined(separator: "\n")
        }
        finishedSessionDir = dir
        process = nil
        stdinPipe = nil
        startDate = nil
        stopDate = nil
        finishingElapsed = 0
        finishingStatus = ""
        liveDirURL = nil
        phase = .idle
        onFinished?(dir)
    }
}
