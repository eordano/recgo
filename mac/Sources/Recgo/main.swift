import AppKit
import SwiftUI
import Combine

final class AppDelegate: NSObject, NSApplicationDelegate {
    var statusBar: StatusBarController?
    private var closeAfterStop = false
    private var quitWatch: AnyCancellable?
    private var quitReplied = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        NSApp.appearance = NSAppearance(named: .darkAqua)
        statusBar = StatusBarController()
        Hotkeys.shared.register()

        // No main menu means no Close or Quit items, so ⌘W and ⌘Q need
        // wiring by hand.
        NSEvent.addLocalMonitorForEvents(matching: .keyDown) { event in
            let mods = event.modifierFlags
            guard mods.contains(.command),
                  mods.isDisjoint(with: [.option, .control, .shift])
            else { return event }
            switch event.charactersIgnoringModifiers?.lowercased() {
            case "w":
                guard let win = NSApp.keyWindow else { return event }
                if win.styleMask.contains(.closable) {
                    win.performClose(nil)
                } else {
                    win.orderOut(nil)
                }
                return nil
            case "q":
                NSApp.terminate(nil)
                return nil
            default:
                return event
            }
        }

        let recorder = Recorder.shared
        recorder.onStarted = {
            Windows.shared.showHUD()
            Windows.shared.showLiveWindow()
        }
        MeetWatcher.shared.start()
        recorder.onFinished = { [weak self] dir in
            Windows.shared.closeHUD()
            Windows.shared.closeLiveWindow()
            if self?.closeAfterStop == true {
                self?.closeAfterStop = false
                return
            }
            var isDir: ObjCBool = false
            if let dir, FileManager.default.fileExists(atPath: dir.path, isDirectory: &isDir), !isDir.boolValue {
                // Audio only: recgo wrote one file, not a session folder;
                // the Library lists folders, so point at the file instead.
                NSWorkspace.shared.activateFileViewerSelecting([dir])
            } else if let dir {
                Windows.shared.showLibrary(selecting: dir.lastPathComponent)
            } else if !recorder.lastError.isEmpty {
                let alert = NSAlert()
                alert.messageText = "Recording did not finish"
                var info = recorder.lastError
                if info.contains(":9222") {
                    info += "\n\nBrowser and Tab modes attach to Chrome's debug port. "
                        + "Start Chrome with --remote-debugging-port=9222 first."
                }
                alert.informativeText = info
                alert.runModal()
            }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        MeetWatcher.shared.shutdown()
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        let recorder = Recorder.shared
        let recording = recorder.isRecording
        let finishing = recorder.phase == .finishing
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = recording ? "Stop this session?"
            : finishing ? "The last session is still packing" : "Quit Recgo?"
        alert.informativeText = recording
            ? "Closing the session stops the recording and packs it; "
                + "Recgo stays in the menu bar."
            : finishing
            ? "The recorder is transcribing and packing what you just captured. "
                + "Quit waits for it to land (or force-stops after 15 seconds)."
            : "Closing keeps Recgo in the menu bar. Quitting removes it."
        alert.addButton(withTitle: recording ? "Close Session & Windows" : "Close Windows")
        alert.addButton(withTitle: "Cancel")
        alert.addButton(withTitle: "Quit")

        switch alert.runModal() {
        case .alertFirstButtonReturn:
            if recording {
                closeAfterStop = true
                recorder.stop()
            }
            Windows.shared.closeAuxiliary()
            return .terminateCancel
        case .alertThirdButtonReturn:
            if recorder.isBusy {
                closeAfterStop = true
                if recording { recorder.stop() }
                waitForPackThenQuit()
                return .terminateLater
            }
            return .terminateNow
        default:
            return .terminateCancel
        }
    }

    // Quit must not orphan a packing recorder: wait for it to reach idle,
    // and if it wedges, force-stop (raw files survive) before replying.
    private func waitForPackThenQuit() {
        quitReplied = false
        quitWatch = Recorder.shared.$phase
            .receive(on: DispatchQueue.main)
            .filter { $0 == .idle }
            .first()
            .sink { [weak self] _ in self?.replyQuit() }
        DispatchQueue.main.asyncAfter(deadline: .now() + 15) { [weak self] in
            guard let self, !self.quitReplied else { return }
            Recorder.shared.forceStop()
            DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
                self.replyQuit()
            }
        }
    }

    private func replyQuit() {
        guard !quitReplied else { return }
        quitReplied = true
        quitWatch = nil
        NSApp.reply(toApplicationShouldTerminate: true)
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
