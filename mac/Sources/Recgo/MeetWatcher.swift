import AppKit
import Combine
import Foundation

// The same Go detector feeds the Linux and Mac shells. All consent and recorder
// ownership changes happen on the main thread, including process output.
final class MeetWatcher: NSObject, NSWindowDelegate {
    static let shared = MeetWatcher()
    struct Call: Decodable { let id: String; let url: String; let present: Bool; let title: String? }
    struct Snapshot: Decodable { let available: Bool; let calls: [Call] }

    private var process: Process?
    private var output: Pipe?
    private var buffer = Data()
    private var timer: Timer?
    private var phaseWatch: AnyCancellable?
    private var calls: [String: Call] = [:]
    private var prompted = Set<String>()
    private var owned: String?
    private var alert: NSAlert?
    private var promptID: String?
    private var lastUpdate = Date.distantPast
    private(set) var status = "disabled"

    func start() {
        phaseWatch = Recorder.shared.$phase.sink { [weak self] phase in
            if phase != .idle { self?.closePrompt() }
            if phase != .recording { self?.owned = nil }
        }
        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
            self?.refresh()
        }
        refresh()
    }

    private func refresh() {
        guard AppSettings.shared.meetPrompt else {
            closePrompt()
            owned = nil
            calls = [:]
            process?.terminate()
            status = "disabled"
            return
        }
        if process == nil {
            guard let binary = AppSettings.shared.resolveBinary("recgo-meet-watch") else {
                status = "helper missing"
                return
            }
            let p = Process()
            let pipe = Pipe()
            p.executableURL = binary
            p.standardOutput = pipe
            p.standardError = FileHandle.nullDevice
            pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
                let data = handle.availableData
                if data.isEmpty { return }
                DispatchQueue.main.async { self?.read(data) }
            }
            p.terminationHandler = { [weak self] _ in
                DispatchQueue.main.async {
                    guard let self, self.process === p else { return }
                    pipe.fileHandleForReading.readabilityHandler = nil
                    self.process = nil
                    self.output = nil
                    self.calls = [:]
                    self.closePrompt()
                    self.status = "detector stopped"
                }
            }
            buffer = Data()
            do {
                try p.run()
                process = p
                output = pipe
                status = "connecting"
            } catch {
                pipe.fileHandleForReading.readabilityHandler = nil
                status = "could not start detector"
            }
        }
        if Date().timeIntervalSince(lastUpdate) > 8 {
            calls = [:]
            closePrompt()
            status = "browser unavailable"
        }
    }

    private func read(_ data: Data) {
        buffer.append(data)
        while let newline = buffer.firstIndex(of: 10) {
            let line = Data(buffer[..<newline])
            buffer.removeSubrange(...newline)
            guard let snapshot = try? JSONDecoder().decode(Snapshot.self, from: line) else { continue }
            update(snapshot)
        }
    }

    private func update(_ snapshot: Snapshot) {
        guard AppSettings.shared.meetPrompt else { return }
        lastUpdate = Date()
        status = snapshot.available ? "watching" : "browser unavailable"
        calls = snapshot.available ? Dictionary(snapshot.calls.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a }) : [:]
        if snapshot.available {
            prompted.formIntersection(Set(calls.keys))
            if let id = owned, calls[id] == nil {
                owned = nil
                Recorder.shared.stop()
            }
        }
        if let id = promptID, calls[id]?.present != true { closePrompt() }
        guard snapshot.available, alert == nil, !Recorder.shared.isBusy else { return }
        for call in snapshot.calls where call.present && !prompted.contains(call.id) {
            prompted.insert(call.id)
            showPrompt(call)
            break
        }
    }

    private func showPrompt(_ call: Call) {
        let box = NSAlert()
        box.messageText = "Record this Meet call?"
        box.informativeText = call.url + "\n\nRecords your microphone and system audio, then stops when you leave. Uses your transcription and sharing settings. System audio needs a configured loopback device on this Mac."
        // Not now is deliberately the default, including Return and Escape.
        let no = box.addButton(withTitle: "Not now")
        no.target = self
        no.action = #selector(decline)
        no.keyEquivalent = "\r"
        let yes = box.addButton(withTitle: "Record call")
        yes.target = self
        yes.action = #selector(accept)
        yes.keyEquivalent = ""
        alert = box
        promptID = call.id
        box.window.delegate = self
        box.window.level = .floating
        box.window.center()
        box.window.orderFrontRegardless()
    }

    @objc private func decline() { closePrompt() }

    @objc private func accept() {
        let id = promptID
        closePrompt()
        guard let id, AppSettings.shared.meetPrompt, !Recorder.shared.isBusy,
              Date().timeIntervalSince(lastUpdate) < 8, calls[id]?.present == true else { return }
        // The detector suggests the recording name: the meeting's title
        // when Meet shows one, else meet-<room code>.
        Recorder.shared.start(.audio, meeting: true, name: calls[id]?.title ?? "")
        if Recorder.shared.isRecording { owned = id }
    }

    private func closePrompt() {
        alert?.window.orderOut(nil)
        alert = nil
        promptID = nil
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { closePrompt(); return true }

    func shutdown() {
        timer?.invalidate()
        timer = nil
        phaseWatch = nil
        closePrompt()
        output?.fileHandleForReading.readabilityHandler = nil
        process?.terminate()
    }
}
