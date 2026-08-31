import AppKit
import SwiftUI
import Combine

enum Actions {
    static func start(_ mode: RecordMode) {
        guard !Recorder.shared.isBusy else { return }
        if mode == .browser || mode == .tab {
            CDP.shared.refresh { up in
                guard !Recorder.shared.isBusy else { return }
                if up {
                    Recorder.shared.start(mode)
                } else {
                    let alert = NSAlert()
                    alert.messageText = "\(mode.label) mode needs a browser on CDP"
                    alert.informativeText = "Start Chrome with "
                        + "--remote-debugging-port=9222, then try again."
                    alert.runModal()
                }
            }
            return
        }
        Recorder.shared.start(mode)
    }

    static func stop() { Recorder.shared.stop() }
    static func mark() { Recorder.shared.mark() }
}

final class StatusBarController: NSObject {
    private let item: NSStatusItem
    private var panel: NSPanel?
    private var clickMonitor: Any?
    private var cancellables: Set<AnyCancellable> = []

    static weak var current: StatusBarController?

    override init() {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()
        StatusBarController.current = self

        if let button = item.button {
            button.action = #selector(togglePopover)
            button.target = self
        }
        refresh()

        Recorder.shared.$phase.combineLatest(Recorder.shared.$elapsed)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _, _ in self?.refresh() }
            .store(in: &cancellables)
    }

    private func refresh() {
        guard let button = item.button else { return }
        let phase = Recorder.shared.phase
        let recording = phase == .recording
        let finishing = phase == .finishing
        let name = recording || finishing ? "record.circle.fill" : "record.circle"
        let image = NSImage(
            systemSymbolName: name, accessibilityDescription: "Recgo")?
            .withSymbolConfiguration(.init(pointSize: 15, weight: .semibold))
        image?.isTemplate = !(recording || finishing)
        button.image = image
        button.contentTintColor = recording ? NSColor(Theme.accent)
            : finishing ? NSColor(Theme.amber) : nil

        if recording && AppSettings.shared.menuTimer {
            button.imagePosition = .imageLeading
            button.attributedTitle = NSAttributedString(
                string: " " + clockString(Recorder.shared.elapsed),
                attributes: [
                    .font: NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .semibold),
                    .foregroundColor: NSColor(Theme.pink),
                ])
        } else if finishing {
            button.imagePosition = .imageLeading
            button.attributedTitle = NSAttributedString(
                string: " finishing…",
                attributes: [
                    .font: NSFont.systemFont(ofSize: 12, weight: .semibold),
                    .foregroundColor: NSColor(Theme.amber),
                ])
        } else {
            button.title = ""
            button.imagePosition = .imageOnly
        }
    }

    @objc private func togglePopover() {
        if panel?.isVisible == true {
            closePopover()
            return
        }
        // The status button's own window gives the exact screen anchor; a
        // plain panel avoids NSPopover's arrow and its flaky anchoring.
        guard let buttonWindow = item.button?.window else { return }
        let host = NSHostingController(
            rootView: PopoverView()
                .environmentObject(Recorder.shared)
                .environmentObject(AppSettings.shared))
        let size = host.view.fittingSize
        let anchor = buttonWindow.frame
        // On a crowded (notched) menu bar the status item can be overflowed
        // and its window reports an off-screen frame; anchor to the screen's
        // top-right corner instead of trusting it.
        let vis = (buttonWindow.screen ?? NSScreen.main)?.visibleFrame
            ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        var x = anchor.maxX - size.width
        if anchor.minY <= 0 || x < vis.minX || anchor.maxX > vis.maxX + 1 {
            x = vis.maxX - size.width - 8
        }
        let p = NSPanel(
            contentRect: NSRect(
                x: x, y: vis.maxY - size.height - 6,
                width: size.width, height: size.height),
            styleMask: [.borderless, .nonactivatingPanel, .fullSizeContentView],
            backing: .buffered, defer: false)
        p.backgroundColor = .clear
        p.isOpaque = false
        p.hasShadow = true
        p.level = .popUpMenu
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        p.contentView = host.view
        host.view.wantsLayer = true
        host.view.layer?.cornerRadius = 20
        host.view.layer?.masksToBounds = true
        p.orderFrontRegardless()
        panel = p

        clickMonitor = NSEvent.addGlobalMonitorForEvents(
            matching: [.leftMouseDown, .rightMouseDown]) { [weak self] _ in
            self?.closePopover()
        }
    }

    func closePopover() {
        panel?.orderOut(nil)
        panel = nil
        if let m = clickMonitor {
            NSEvent.removeMonitor(m)
            clickMonitor = nil
        }
    }
}

struct PopoverView: View {
    @EnvironmentObject var recorder: Recorder
    @EnvironmentObject var settings: AppSettings
    @ObservedObject var cdp = CDP.shared
    private let cdpTick = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if recorder.isRecording {
                recordingHeader
                recordingRows
            } else if recorder.phase == .finishing {
                finishingHeader
                finishingRows
            } else {
                idleRows
            }
            divider
            Text("Audio")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.faint)
                .padding(.horizontal, 12).padding(.top, 2).padding(.bottom, 4)
            trackRow(
                name: settings.micDevice.isEmpty ? "Microphone" : settings.micDevice,
                hint: settings.micDevice.isEmpty ? "system default" : "named device",
                on: settings.captureMic
            ) { settings.captureMic.toggle() }
            HStack {
                Text("System audio")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Theme.faint)
                Spacer()
                Text("Audio-only mode")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Theme.faint)
            }
            .padding(.horizontal, 12).padding(.vertical, 7)
            divider
            menuRow("Library…", shortcut: "⌘L") {
                StatusBarController.current?.closePopover()
                Windows.shared.showLibrary()
            }
            menuRow("Settings…", shortcut: "⌘,") {
                StatusBarController.current?.closePopover()
                Windows.shared.showSettings()
            }
            menuRow("Quit Recgo", shortcut: "") {
                NSApp.terminate(nil)
            }
        }
        .padding(8)
        .frame(width: 352)
        .background(GlassBackground())
        .onAppear { CDP.shared.refresh() }
        .onReceive(cdpTick) { _ in CDP.shared.refresh() }
    }

    private var divider: some View {
        Rectangle().fill(Theme.hairline).frame(height: 1)
            .padding(.horizontal, 4).padding(.vertical, 8)
    }

    private var idleRows: some View {
        VStack(spacing: 2) {
            ForEach(RecordMode.allCases) { mode in
                let needsCDP = mode == .browser || mode == .tab
                let enabled = !needsCDP || cdp.available
                HoverRow(action: {
                    StatusBarController.current?.closePopover()
                    Actions.start(mode)
                }) {
                    HStack(spacing: 11) {
                        Image(systemName: iconName(mode))
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.text)
                            .frame(width: 26, height: 26)
                            .background(
                                RoundedRectangle(cornerRadius: 8).fill(Theme.control))
                        Text(mode == .tab ? "This Tab…" : mode.label)
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundStyle(Theme.text)
                        Spacer()
                        Text(enabled ? mode.hotkeyLabel : "no browser on :9222")
                            .font(.system(size: enabled ? 12 : 11, weight: .semibold))
                            .foregroundStyle(Theme.faint)
                    }
                    .padding(.horizontal, 12).padding(.vertical, 8)
                }
                .disabled(!enabled)
                .opacity(enabled ? 1 : 0.38)
                .help(enabled ? "" :
                    "Start Chrome with --remote-debugging-port=9222 to record the browser")
            }
        }
        .padding(.top, 4)
    }

    private var recordingHeader: some View {
        HStack(spacing: 9) {
            RecDot()
            Text(clockString(recorder.elapsed))
                .font(.system(size: 15, weight: .bold).monospacedDigit())
                .foregroundStyle(Theme.text)
            Text(recorder.mode.label)
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(Theme.muted)
            Spacer()
        }
        .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 11)
    }

    private var recordingRows: some View {
        VStack(spacing: 2) {
            menuRow("Mark this moment", shortcut: Hotkeys.label(forKey: "shortcutMark"),
                    icon: markIcon) {
                Actions.mark()
            }
            menuRow(
                Windows.shared.hudVisible ? "Hide HUD" : "Show HUD",
                shortcut: Hotkeys.label(forKey: "shortcutToggleHUD")
            ) {
                Windows.shared.toggleHUD()
            }
            menuRow(
                Windows.shared.liveVisible ? "Hide live session" : "Show live session",
                shortcut: ""
            ) {
                Windows.shared.toggleLiveWindow()
            }
            HoverRow(base: Theme.accent.opacity(0.14), hover: Theme.accent.opacity(0.24),
                     action: { StatusBarController.current?.closePopover(); Actions.stop() }) {
                HStack(spacing: 11) {
                    RoundedRectangle(cornerRadius: 2).fill(Theme.accent)
                        .frame(width: 11, height: 11).frame(width: 26)
                    Text("Stop and open session")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(Theme.pink)
                    Spacer()
                    let stopLabel = Hotkeys.label(forKey: "shortcutStop")
                    if !stopLabel.isEmpty {
                        Text(stopLabel)
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.pink.opacity(0.7))
                    }
                }
                .padding(.horizontal, 12).padding(.vertical, 8)
            }
        }
    }

    private var finishingHeader: some View {
        HStack(spacing: 9) {
            ProgressView().controlSize(.small)
            Text(clockString(recorder.elapsed))
                .font(.system(size: 15, weight: .bold).monospacedDigit())
                .foregroundStyle(Theme.secondary)
            Text("Finishing \(recorder.mode.label.lowercased())")
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(Theme.amber)
            Spacer()
            Text(clockString(recorder.finishingElapsed))
                .font(.system(size: 12, weight: .semibold).monospacedDigit())
                .foregroundStyle(Theme.faint)
        }
        .padding(.horizontal, 12).padding(.top, 10).padding(.bottom, 11)
    }

    private var finishingRows: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Capture stopped — transcribing and packing the session. "
                 + "It opens in the Library when done.")
                .font(.system(size: 12))
                .foregroundStyle(Theme.muted)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, 12).padding(.bottom, 4)
            if !recorder.finishingStatus.isEmpty {
                Text(recorder.finishingStatus)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(Theme.faint)
                    .lineLimit(1)
                    .padding(.horizontal, 12).padding(.bottom, 6)
            }
            if recorder.finishingElapsed > 8 {
                HoverRow(base: Theme.accent.opacity(0.14),
                         hover: Theme.accent.opacity(0.24),
                         action: { recorder.forceStop() }) {
                    HStack(spacing: 11) {
                        Image(systemName: "xmark.octagon")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.pink)
                            .frame(width: 26)
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Force stop")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundStyle(Theme.pink)
                            Text("skips packing; raw files stay on disk")
                                .font(.system(size: 11))
                                .foregroundStyle(Theme.faint)
                        }
                        Spacer()
                    }
                    .padding(.horizontal, 12).padding(.vertical, 8)
                }
            }
        }
    }

    private var markIcon: AnyView {
        AnyView(
            RoundedRectangle(cornerRadius: 2).fill(Theme.amber)
                .frame(width: 10, height: 10).rotationEffect(.degrees(45)).frame(width: 26))
    }

    private func menuRow(
        _ title: String, shortcut: String, icon: AnyView? = nil,
        action: @escaping () -> Void
    ) -> some View {
        HoverRow(action: action) {
            HStack(spacing: 11) {
                if let icon { icon }
                Text(title)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.text)
                Spacer()
                if !shortcut.isEmpty {
                    Text(shortcut)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(Theme.faint)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
        }
    }

    private func trackRow(
        name: String, hint: String, on: Bool, toggle: @escaping () -> Void
    ) -> some View {
        HoverRow(action: toggle) {
            HStack(spacing: 11) {
                ZStack {
                    RoundedRectangle(cornerRadius: 6)
                        .stroke(on ? Theme.accent : Color.white.opacity(0.22), lineWidth: 1.7)
                        .background(
                            RoundedRectangle(cornerRadius: 6).fill(on ? Theme.accent : .clear))
                        .frame(width: 18, height: 18)
                    if on {
                        Image(systemName: "checkmark")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundStyle(Theme.text)
                    }
                }
                Text(name)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(on ? Theme.text : Theme.faint)
                    .lineLimit(1)
                Spacer()
                Text(hint)
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Theme.faint)
            }
            .padding(.horizontal, 12).padding(.vertical, 7)
        }
    }

    private func iconName(_ mode: RecordMode) -> String {
        switch mode {
        case .screen: return "rectangle"
        case .browser: return "globe"
        case .tab: return "macwindow"
        case .audio: return "waveform"
        }
    }
}

struct RecDot: View {
    @State private var dim = false
    var size: CGFloat = 9

    var body: some View {
        Circle().fill(Theme.accent)
            .frame(width: size, height: size)
            .opacity(dim ? 0.28 : 1)
            .animation(.easeInOut(duration: 0.8).repeatForever(), value: dim)
            .onAppear { dim = true }
    }
}
