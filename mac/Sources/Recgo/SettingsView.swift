import SwiftUI

private enum Pane: String, CaseIterable, Identifiable {
    case general = "General"
    case recording = "Recording"
    case audio = "Audio"
    case transcription = "Transcription"
    case privacy = "Privacy"
    case portal = "Agents & Portal"
    case shortcuts = "Shortcuts"
    case library = "Library"
    var id: String { rawValue }
}

struct SettingsView: View {
    @EnvironmentObject var settings: AppSettings
    @State private var pane: Pane = .general

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                Color.clear.frame(height: 26)
                ForEach(Pane.allCases) { p in
                    HoverRow(radius: 9,
                             base: pane == p ? Theme.accent.opacity(0.16) : .clear,
                             hover: Color.white.opacity(0.07),
                             action: { pane = p }) {
                        Text(p.rawValue)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(pane == p ? Theme.text : Theme.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 11).padding(.vertical, 8)
                    }
                }
                Spacer()
            }
            .padding(10)
            .frame(width: 184)
            Rectangle().fill(Theme.hairline).frame(width: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Color.clear.frame(height: 20)
                    paneBody
                }
                .padding(.horizontal, 28).padding(.bottom, 32)
            }
        }
        .background(Theme.bg)
        .frame(width: 860, height: 600)
    }

    @ViewBuilder private var paneBody: some View {
        switch pane {
        case .general: general
        case .recording: recording
        case .audio: audio
        case .transcription: transcription
        case .privacy: privacy
        case .portal: portal
        case .shortcuts: shortcuts
        case .library: library
        }
    }

    private var general: some View {
        VStack(spacing: 0) {
            row("Session folder", caption: settings.outRoot, mono: true) {
                AnyView(chooseButton)
            }
            toggleRow("Launch at login",
                      caption: "Adds the menu bar item only — no window opens.",
                      isOn: Binding(
                        get: { settings.launchAtLogin },
                        set: { settings.launchAtLogin = $0 }))
            toggleRow("Show elapsed time in the menu bar",
                      caption: "Some people find a running timer stressful.",
                      isOn: $settings.menuTimer)
            toggleRow("Title sessions automatically",
                      caption: "A local model reads the narration and names the folder.",
                      isOn: $settings.autoTitle, last: true)
        }
    }

    private var chooseButton: some View {
        Button("Choose…") {
            let panel = NSOpenPanel()
            panel.canChooseDirectories = true
            panel.canChooseFiles = false
            panel.canCreateDirectories = true
            if panel.runModal() == .OK, let url = panel.url {
                settings.outRoot = url.path
            }
        }
        .buttonStyle(.plain)
        .font(.system(size: 13, weight: .semibold))
        .foregroundStyle(Theme.text)
        .padding(.horizontal, 13).padding(.vertical, 7)
        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.control))
    }

    private var recording: some View {
        VStack(spacing: 0) {
            row("Default mode", caption: "Used by the hotkeys' fallback and Shortcuts.") {
                AnyView(
                    HStack(spacing: 2) {
                        ForEach(RecordMode.allCases) { m in
                            let active = settings.defaultMode == m
                            Button {
                                settings.defaultMode = m
                            } label: {
                                Text(m == .audio ? "Audio" : m.label)
                                    .font(.system(size: 12, weight: .semibold))
                                    .foregroundStyle(active ? Theme.text : Theme.muted)
                                    .padding(.horizontal, 11).padding(.vertical, 6)
                                    .background(RoundedRectangle(cornerRadius: 8)
                                        .fill(active ? Theme.accent : .clear))
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(2)
                    .background(RoundedRectangle(cornerRadius: 10)
                        .fill(Color.black.opacity(0.3))))
            }
            toggleRow("Screenshot on every click",
                      caption: "Needs Accessibility. Recording still works without it.",
                      isOn: $settings.clickShots)
            toggleRow("Also screenshot focus changes and new windows",
                      caption: "Dialogs and app switches become anchors in the document.",
                      isOn: $settings.focusShots, last: true)
        }
    }

    private var audio: some View {
        VStack(spacing: 0) {
            toggleRow("Capture the microphone",
                      caption: "Narration is the point — off means a silent session.",
                      isOn: $settings.captureMic)
            row("Microphone device",
                caption: "Blank uses the system default source.") {
                AnyView(field($settings.micDevice, placeholder: "default", width: 220))
            }
            banner(
                title: "System audio needs BlackHole",
                body: "Every mode records the microphone. To include system audio, "
                    + "point the device at a BlackHole loopback (or an aggregate "
                    + "of mic + loopback).",
                tint: Theme.amber)
        }
    }

    private var transcription: some View {
        VStack(spacing: 0) {
            row("Backend",
                caption: "auto prefers a local whisper model and only then goes remote.") {
                AnyView(
                    Picker("", selection: $settings.sttBackend) {
                        ForEach(["auto", "local", "remote", "none"], id: \.self) {
                            Text($0)
                        }
                    }
                    .labelsHidden().frame(width: 120))
            }
            row("Whisper model", caption: "Blank discovers a ggml model on disk.") {
                AnyView(field($settings.whisperModel, placeholder: "discovered", width: 260))
            }
            row("whisper.cpp binary", caption: "Blank finds whisper-cli on PATH.") {
                AnyView(field($settings.whisperBin, placeholder: "whisper-cli", width: 260))
            }
            row("Language", caption: "Blank autodetects; costs about a second at start.") {
                AnyView(field($settings.sttLanguage, placeholder: "auto", width: 120))
            }
            toggleRow("Show narration live in the HUD",
                      caption: "Proof that speech is being heard, decoded as you speak.",
                      isOn: $settings.liveNarrationHUD, last: true)
        }
    }

    private var privacy: some View {
        VStack(spacing: 0) {
            HStack(spacing: 14) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Never upload anything")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundStyle(settings.neverUpload ? Theme.green : Theme.amber)
                    Text("One switch. Remote transcription, portal and sync all go dark "
                         + "and stay dark.")
                        .font(.system(size: 12)).foregroundStyle(Theme.secondary)
                }
                Spacer()
                Toggle("", isOn: $settings.neverUpload)
                    .toggleStyle(RecgoToggleStyle())
            }
            .padding(16)
            .background(RoundedRectangle(cornerRadius: 14)
                .fill((settings.neverUpload ? Theme.green : Theme.amber).opacity(0.10)))
            .overlay(RoundedRectangle(cornerRadius: 14)
                .stroke((settings.neverUpload ? Theme.green : Theme.amber).opacity(0.27)))
            .padding(.bottom, 8)

            Group {
                toggleRow("Sync finished sessions to a host",
                          caption: settings.syncTarget.isEmpty
                            ? "PUSHES THE WHOLE SESSION over ssh when set."
                            : settings.syncTarget,
                          mono: !settings.syncTarget.isEmpty,
                          isOn: $settings.syncEnabled)
                row("Sync target", caption: "user@host:/path — handed to rsync over ssh.") {
                    AnyView(field($settings.syncTarget,
                                  placeholder: "host:/srv/sessions", width: 260))
                }
                row("Sync ssh key",
                    caption: "A dedicated non-touch identity, tried before the "
                        + "config ones — a YubiKey key here blocks every session end.") {
                    AnyView(field($settings.syncKey,
                                  placeholder: "~/.ssh/id_sync", width: 260))
                }
            }
            .opacity(settings.neverUpload ? 0.38 : 1)
            .disabled(settings.neverUpload)
        }
    }

    private var portal: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("A portal room lets an agent read SESSION.live.md while you talk. "
                 + "The room name is the only credential — EXPOSES THE OUTPUT ROOT.")
                .font(.system(size: 12)).foregroundStyle(Theme.faint)
                .padding(.bottom, 12)
            Group {
                toggleRow("Share live with agent",
                          caption: "Opens the portal room on every session start.",
                          isOn: $settings.portalEnabled)
                row("Portal URL", caption: "wss endpoint of the portal service.") {
                    AnyView(field($settings.portalURL,
                                  placeholder: "wss://portal.example/ws", width: 280))
                }
                row("Room", caption: "Blank derives a room name per session.") {
                    AnyView(field($settings.portalRoom,
                                  placeholder: "walk-lantern-42", width: 220))
                }
            }
            .opacity(settings.neverUpload ? 0.38 : 1)
            .disabled(settings.neverUpload)
            if settings.neverUpload {
                Text("Blocked by “Never upload anything”.")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.amber)
                    .padding(.top, 10)
            }
        }
    }

    private var shortcuts: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("No key is grabbed by default. A chord like cmd+shift+1 binds "
                 + "the verb system-wide; blank leaves it to the menu.")
                .font(.system(size: 12)).foregroundStyle(Theme.faint)
                .padding(.bottom, 12)
            shortcutRow("Record screen", $settings.shortcutRecordScreen)
            shortcutRow("Record browser", $settings.shortcutRecordBrowser)
            shortcutRow("Record this tab", $settings.shortcutRecordTab)
            shortcutRow("Record audio only", $settings.shortcutRecordAudio)
            shortcutRow("Mark this moment", $settings.shortcutMark)
            shortcutRow("Hide or show the HUD", $settings.shortcutToggleHUD)
            shortcutRow("Stop and open session", $settings.shortcutStop)
            shortcutRow("Open Library", $settings.shortcutOpenLibrary, last: true)
        }
    }

    private func shortcutRow(
        _ name: String, _ spec: Binding<String>, last: Bool = false
    ) -> some View {
        let parsed = Hotkeys.parse(spec.wrappedValue)
        return HStack(spacing: 12) {
            Text(name).font(.system(size: 14, weight: .semibold))
                .foregroundStyle(Theme.text)
            Spacer()
            if !spec.wrappedValue.isEmpty {
                Text(parsed?.label ?? "not a chord")
                    .font(.system(size: 13, weight: .semibold).monospacedDigit())
                    .foregroundStyle(parsed == nil ? Theme.amber : Theme.secondary)
                    .padding(.horizontal, 11).padding(.vertical, 5)
                    .background(RoundedRectangle(cornerRadius: 8).fill(Theme.control))
                    .overlay(RoundedRectangle(cornerRadius: 8)
                        .stroke(Color.white.opacity(0.10)))
            }
            field(spec, placeholder: "none", width: 150)
        }
        .padding(.vertical, 11)
        .overlay(alignment: .bottom) {
            if !last { Rectangle().fill(Color.white.opacity(0.07)).frame(height: 1) }
        }
        .onChange(of: spec.wrappedValue) { Hotkeys.shared.register() }
    }

    private var library: some View {
        VStack(spacing: 0) {
            row("Binaries folder",
                caption: "Where the recgo CLIs live. Blank searches the bundle and PATH.") {
                AnyView(field($settings.binDir, placeholder: "auto", width: 260))
            }
            row("Storage used", caption: storageLine) {
                AnyView(
                    Button("Reveal in Finder") {
                        NSWorkspace.shared.open(settings.outRootURL)
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Theme.text)
                    .padding(.horizontal, 13).padding(.vertical, 7)
                    .background(RoundedRectangle(cornerRadius: 9).fill(Theme.control)))
            }
            row("HTML index",
                caption: "recgo-sessions writes a self-contained index.html viewer.") {
                AnyView(
                    Button("Regenerate") {
                        if let bin = settings.resolveBinary("recgo-sessions") {
                            let p = Process()
                            p.executableURL = bin
                            p.arguments = ["-dir", settings.outRootURL.path]
                            try? p.run()
                        }
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Theme.text)
                    .padding(.horizontal, 13).padding(.vertical, 7)
                    .background(RoundedRectangle(cornerRadius: 9).fill(Theme.control)))
            }
        }
    }

    private var storageLine: String {
        let store = LibraryStore.shared
        if store.sessions.isEmpty { store.reload() }
        return "\(store.sessions.count) sessions in \(settings.outRootURL.lastPathComponent)"
    }

    // MARK: row scaffolding, demo-styled

    private func row(
        _ title: String, caption: String, mono: Bool = false,
        @ViewBuilder control: () -> AnyView
    ) -> some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.text)
                Text(caption)
                    .font(.system(size: 12, design: mono ? .monospaced : .default))
                    .foregroundStyle(caption.contains("PUSHES") || caption.contains("UPLOADS")
                                     ? Theme.pink : Theme.faint)
            }
            Spacer()
            control()
        }
        .padding(.vertical, 14)
        .overlay(alignment: .bottom) {
            Rectangle().fill(Color.white.opacity(0.07)).frame(height: 1)
        }
    }

    private func toggleRow(
        _ title: String, caption: String, mono: Bool = false,
        isOn: Binding<Bool>, last: Bool = false
    ) -> some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.text)
                Text(caption)
                    .font(.system(size: 12, design: mono ? .monospaced : .default))
                    .foregroundStyle(caption.contains("PUSHES") ? Theme.pink : Theme.faint)
            }
            Spacer()
            Toggle("", isOn: isOn).toggleStyle(RecgoToggleStyle())
        }
        .padding(.vertical, 14)
        .overlay(alignment: .bottom) {
            if !last { Rectangle().fill(Color.white.opacity(0.07)).frame(height: 1) }
        }
    }

    private func field(
        _ binding: Binding<String>, placeholder: String, width: CGFloat
    ) -> some View {
        TextField(placeholder, text: binding)
            .textFieldStyle(.plain)
            .font(.system(size: 13, design: .monospaced))
            .foregroundStyle(Theme.text)
            .padding(.horizontal, 10).padding(.vertical, 6)
            .frame(width: width)
            .background(RoundedRectangle(cornerRadius: 8).fill(Color.white.opacity(0.07)))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.white.opacity(0.09)))
    }

    private func banner(title: String, body: String, tint: Color) -> some View {
        HStack(spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.system(size: 14, weight: .semibold)).foregroundStyle(tint)
                Text(body).font(.system(size: 12)).foregroundStyle(Theme.secondary)
            }
            Spacer()
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 14).fill(tint.opacity(0.10)))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(tint.opacity(0.28)))
        .padding(.top, 18)
    }
}

struct RecgoToggleStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        Button { configuration.isOn.toggle() } label: {
            ZStack(alignment: configuration.isOn ? .trailing : .leading) {
                Capsule()
                    .fill(configuration.isOn ? Theme.accent : Color.white.opacity(0.16))
                    .frame(width: 40, height: 23)
                Circle().fill(Theme.text)
                    .frame(width: 19, height: 19)
                    .shadow(color: .black.opacity(0.4), radius: 1.5, y: 1)
                    .padding(2)
            }
        }
        .buttonStyle(.plain)
        .animation(.spring(duration: 0.18), value: configuration.isOn)
    }
}
