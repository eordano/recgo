import SwiftUI
import AVFoundation

final class LibraryStore: ObservableObject {
    static let shared = LibraryStore()

    @Published var sessions: [LibrarySession] = []
    @Published var selectedID: String?
    @Published var search = ""
    @Published var modeFilter: String?
    @Published var reading = false
    @Published var previewIndex: Int?
    @Published var toast = ""
    // Session time under the pointer: the waveform ghosts a playhead there
    // and the shot strip drifts to the nearest shot. A signal, not a seek.
    @Published var hoverT: Double?
    // Which source the library reads: nil is the recording root, otherwise a
    // browsed folder (typically a mounted remote volume).
    @Published var activeRoot: URL?

    var currentRoot: URL { activeRoot ?? AppSettings.shared.outRootURL }

    var selected: LibrarySession? { sessions.first { $0.id == selectedID } }

    var filtered: [LibrarySession] {
        sessions.filter { s in
            if let mode = modeFilter,
               !s.doc.tool.contains(mode) && !(mode == "screen" && s.doc.tool == "recgo-desktop") {
                return false
            }
            if !search.isEmpty {
                let hay = (s.doc.title + " " + s.doc.events.map(\.text).joined(separator: " "))
                    .lowercased()
                if !hay.contains(search.lowercased()) { return false }
            }
            return true
        }
    }

    func modeCount(_ mode: String) -> Int {
        sessions.filter {
            $0.doc.tool.contains(mode) || (mode == "screen" && $0.doc.tool == "recgo-desktop")
        }.count
    }

    func reload(selecting id: String? = nil) {
        sessions = scanLibrary(root: currentRoot)
        if let id, !sessions.contains(where: { $0.id == id }), activeRoot != nil {
            // A fresh recording always lands in the local root; switch back.
            activeRoot = nil
            sessions = scanLibrary(root: currentRoot)
        }
        if let id, sessions.contains(where: { $0.id == id }) {
            selectedID = id
        } else if selectedID == nil || !sessions.contains(where: { $0.id == selectedID }) {
            selectedID = sessions.first?.id
        }
        previewIndex = nil
        hoverT = nil
    }

    func quickLook(_ url: URL) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/qlmanage")
        p.arguments = ["-p", url.path]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        try? p.run()
    }

    func showToast(_ msg: String) {
        toast = msg
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            if self?.toast == msg { self?.toast = "" }
        }
    }
}

// Audio playback over the session's audio.wav: play/pause, seek, rate, and a
// clip range that can be played and exported.
final class Player: ObservableObject {
    @Published var playing = false
    @Published var t: Double = 0
    @Published var rate: Float = 1
    @Published var clipIn: Double?
    @Published var clipOut: Double?
    @Published var duration: Double = 0
    @Published var bins: [Double] = []
    @Published var hasAudio = false

    private var player: AVAudioPlayer?
    private var timer: Timer?
    private var clipPlay = false
    private(set) var url: URL?

    func load(session: LibrarySession) {
        stopTimer()
        player?.stop()
        player = nil
        playing = false
        t = 0
        clipIn = nil
        clipOut = nil
        clipPlay = false
        let wav = session.url.appendingPathComponent("audio.wav")
        url = wav
        duration = Double(session.doc.durationSec)
        hasAudio = false
        bins = []
        guard FileManager.default.fileExists(atPath: wav.path) else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let loaded = waveformBins(url: wav)
            DispatchQueue.main.async { self?.bins = loaded }
        }
        if let p = try? AVAudioPlayer(contentsOf: wav) {
            p.enableRate = true
            player = p
            duration = max(duration, p.duration)
            hasAudio = true
        }
    }

    func toggle() { playing ? pause() : play() }

    func play(from: Double? = nil) {
        guard let p = player else { return }
        if let from { p.currentTime = min(from, p.duration) }
        p.rate = rate
        p.play()
        playing = true
        startTimer()
    }

    func pause() {
        player?.pause()
        playing = false
        stopTimer()
    }

    func seek(_ to: Double) {
        t = to
        player?.currentTime = min(to, player?.duration ?? to)
    }

    func cycleRate() {
        rate = rate == 1 ? 1.5 : (rate == 1.5 ? 2 : 1)
        player?.rate = rate
    }

    var clipRange: (Double, Double)? {
        guard let a = clipIn, let b = clipOut else { return nil }
        return (min(a, b), max(a, b))
    }

    func playClip() {
        guard let (lo, _) = clipRange else { return }
        clipPlay = true
        play(from: lo)
    }

    func exportClip(session: LibrarySession, done: @escaping (String) -> Void) {
        guard let (lo, hi) = clipRange, let url else { return }
        let clips = session.url.appendingPathComponent("clips")
        try? FileManager.default.createDirectory(at: clips, withIntermediateDirectories: true)
        let name = "clip-\(clockString(lo))-\(clockString(hi)).m4a"
            .replacingOccurrences(of: ":", with: "")
        let out = clips.appendingPathComponent(name)
        try? FileManager.default.removeItem(at: out)
        let asset = AVURLAsset(url: url)
        guard let ex = AVAssetExportSession(
            asset: asset, presetName: AVAssetExportPresetAppleM4A) else {
            done("export failed: no session")
            return
        }
        ex.outputURL = out
        ex.outputFileType = .m4a
        ex.timeRange = CMTimeRange(
            start: CMTime(seconds: lo, preferredTimescale: 600),
            end: CMTime(seconds: hi, preferredTimescale: 600))
        ex.exportAsynchronously {
            DispatchQueue.main.async {
                if ex.status == .completed {
                    done("Clip written to \(out.path)")
                } else {
                    done("export failed: \(ex.error?.localizedDescription ?? "unknown")")
                }
            }
        }
    }

    private func startTimer() {
        stopTimer()
        timer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: true) { [weak self] _ in
            guard let self, let p = self.player else { return }
            self.t = p.currentTime
            if self.clipPlay, let (_, hi) = self.clipRange, p.currentTime >= hi {
                self.clipPlay = false
                self.pause()
                self.seek(hi)
            }
            if !p.isPlaying && self.playing {
                self.playing = false
                self.stopTimer()
            }
        }
    }

    private func stopTimer() {
        timer?.invalidate()
        timer = nil
    }
}

// Every occurrence of the search query gets an amber wash, so a hit is
// visible in place instead of only filtering the list.
func highlightMatches(_ s: String, query: String) -> AttributedString {
    var attr = AttributedString(s)
    let q = query.trimmingCharacters(in: .whitespaces)
    guard !q.isEmpty else { return attr }
    var searchRange = s.startIndex..<s.endIndex
    while let r = s.range(of: q, options: .caseInsensitive, range: searchRange) {
        if let ar = Range(r, in: attr) {
            attr[ar].backgroundColor = Theme.amber.opacity(0.32)
            attr[ar].foregroundColor = Theme.text
        }
        searchRange = r.upperBound..<s.endIndex
    }
    return attr
}

struct LibraryView: View {
    @ObservedObject var store = LibraryStore.shared
    @StateObject private var player = Player()
    @EnvironmentObject var settings: AppSettings

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Rectangle().fill(Theme.hairline).frame(height: 1)
            HStack(spacing: 0) {
                sidebar
                Rectangle().fill(Theme.hairline).frame(width: 1)
                sessionList
                Rectangle().fill(Theme.hairline).frame(width: 1)
                detail
            }
        }
        .background(Theme.bg)
        .overlay(alignment: .bottom) {
            if !store.toast.isEmpty {
                HStack(spacing: 10) {
                    Circle().fill(Theme.amber).frame(width: 9, height: 9)
                    Text(store.toast)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Theme.text)
                }
                .padding(.horizontal, 18).padding(.vertical, 11)
                .background(RoundedRectangle(cornerRadius: 13).fill(Theme.surface.opacity(0.95)))
                .overlay(RoundedRectangle(cornerRadius: 13).stroke(Color.white.opacity(0.16)))
                .padding(.bottom, 20)
                .transition(.opacity)
            }
        }
        .overlay {
            if let idx = store.previewIndex, let s = store.selected {
                ShotPreview(session: s, index: idx)
            }
        }
        .onChange(of: store.selectedID) {
            if let s = store.selected { player.load(session: s) }
        }
        .onAppear {
            store.reload()
            if let s = store.selected { player.load(session: s) }
        }
        .frame(minWidth: 980, minHeight: 620)
    }

    private var toolbar: some View {
        HStack(spacing: 14) {
            Color.clear.frame(width: 58, height: 1)
            Text("Recgo").font(.system(size: 14, weight: .bold)).foregroundStyle(Theme.text)
            Spacer()
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 11)).foregroundStyle(Theme.faint)
                TextField("Search titles and transcripts", text: $store.search)
                    .textFieldStyle(.plain)
                    .font(.system(size: 13))
                    .foregroundStyle(Theme.text)
            }
            .padding(.horizontal, 11).frame(width: 300, height: 30)
            .background(RoundedRectangle(cornerRadius: 9).fill(Color.white.opacity(0.07)))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(Color.white.opacity(0.09)))

            HStack(spacing: 2) {
                segButton("Timeline", active: !store.reading) { store.reading = false }
                segButton("Reading", active: store.reading) { store.reading = true }
            }
            .padding(2)
            .background(RoundedRectangle(cornerRadius: 9).fill(Color.white.opacity(0.07)))

            if settings.syncActive {
                Button {
                    if let s = store.selected,
                       let remote = settings.remoteLocation(for: s.id) {
                        let pb = NSPasteboard.general
                        pb.clearContents()
                        pb.setString(remote, forType: .string)
                        store.showToast("Remote location copied")
                    }
                } label: {
                    Text("Copy remote").font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(Theme.secondary)
                        .padding(.horizontal, 13).padding(.vertical, 6)
                        .background(RoundedRectangle(cornerRadius: 9).fill(Theme.control))
                }
                .buttonStyle(.plain)
                .help("Copies the rsync destination this session synced to")
            }
            Button {
                if let s = store.selected {
                    let pb = NSPasteboard.general
                    pb.clearContents()
                    pb.setString(s.url.path, forType: .string)
                    store.showToast("Path copied")
                }
            } label: {
                Text("Copy path").font(.system(size: 12, weight: .bold))
                    .foregroundStyle(Theme.text)
                    .padding(.horizontal, 13).padding(.vertical, 6)
                    .background(RoundedRectangle(cornerRadius: 9).fill(Theme.accent))
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 16)
        .frame(height: 50)
        .background(Color.white.opacity(0.03))
    }

    private func segButton(_ label: String, active: Bool, action: @escaping () -> Void)
        -> some View {
        Button(action: action) {
            Text(label).font(.system(size: 12, weight: .semibold))
                .foregroundStyle(active ? Theme.text : Theme.muted)
                .padding(.horizontal, 12).padding(.vertical, 5)
                .background(RoundedRectangle(cornerRadius: 7)
                    .fill(active ? Color.white.opacity(0.14) : .clear))
        }
        .buttonStyle(.plain)
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(spacing: 2) {
                sideRow("All Sessions", count: store.sessions.count,
                        active: store.modeFilter == nil) { store.modeFilter = nil }
            }
            VStack(alignment: .leading, spacing: 2) {
                Text("Mode").font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.faint)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                ForEach(["screen", "browser", "tab", "audio"], id: \.self) { m in
                    sideRow(m.capitalized, count: store.modeCount(m),
                            active: store.modeFilter == m) {
                        store.modeFilter = store.modeFilter == m ? nil : m
                    }
                }
            }
            VStack(alignment: .leading, spacing: 2) {
                Text("Sources").font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.faint)
                    .padding(.horizontal, 10).padding(.vertical, 5)
                sourceRow("This Mac", active: store.activeRoot == nil) {
                    store.activeRoot = nil
                    store.reload()
                }
                if !settings.extraRoot.isEmpty {
                    let url = URL(fileURLWithPath:
                        (settings.extraRoot as NSString).expandingTildeInPath)
                    sourceRow(url.lastPathComponent,
                              active: store.activeRoot == url) {
                        store.activeRoot = url
                        store.reload()
                    }
                    .help(url.path)
                }
                sourceRow("Browse folder…", active: false) {
                    let panel = NSOpenPanel()
                    panel.canChooseDirectories = true
                    panel.canChooseFiles = false
                    panel.message = "Pick a folder of recgo sessions — "
                        + "a mounted remote volume works too"
                    if panel.runModal() == .OK, let url = panel.url {
                        settings.extraRoot = url.path
                        store.activeRoot = url
                        store.reload()
                    }
                }
            }
            Spacer()
            Button {
                NSWorkspace.shared.open(store.currentRoot)
            } label: {
                Text("Reveal folder…").font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.secondary)
            }
            .buttonStyle(.plain)
            .padding(.horizontal, 10)
        }
        .padding(.vertical, 14).padding(.horizontal, 10)
        .frame(width: 186)
    }

    private func sourceRow(
        _ label: String, active: Bool, action: @escaping () -> Void
    ) -> some View {
        HoverRow(radius: 9, base: active ? Theme.accent.opacity(0.16) : .clear,
                 hover: Color.white.opacity(0.07), action: action) {
            Text(label).font(.system(size: 13, weight: .semibold))
                .foregroundStyle(active ? Theme.text : Theme.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10).padding(.vertical, 7)
        }
    }

    private func sideRow(
        _ label: String, count: Int, active: Bool, action: @escaping () -> Void
    ) -> some View {
        HoverRow(radius: 9, base: active ? Theme.accent.opacity(0.16) : .clear,
                 hover: Color.white.opacity(0.07), action: action) {
            HStack {
                Text(label).font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(active ? Theme.text : Theme.secondary)
                Spacer()
                Text("\(count)").font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(active ? Theme.muted : Theme.faint)
            }
            .padding(.horizontal, 10).padding(.vertical, 7)
        }
    }

    private var sessionList: some View {
        ScrollView {
            LazyVStack(spacing: 3) {
                ForEach(store.filtered) { s in
                    let active = s.id == store.selectedID
                    HoverRow(base: active ? Theme.accent.opacity(0.16) : .clear,
                             hover: Color.white.opacity(0.05),
                             action: { store.selectedID = s.id }) {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(highlightMatches(
                                s.doc.title.isEmpty ? s.id : s.doc.title,
                                query: store.search))
                                .font(.system(size: 13, weight: .semibold))
                                .foregroundStyle(active ? Theme.text : Theme.secondary)
                                .multilineTextAlignment(.leading)
                            Text(s.meta)
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(Theme.faint)
                                .lineLimit(1)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 12).padding(.vertical, 11)
                    }
                    .overlay(RoundedRectangle(cornerRadius: 11)
                        .stroke(active ? Theme.accent.opacity(0.34) : .clear))
                }
            }
            .padding(8)
        }
        .frame(width: 274)
    }

    @ViewBuilder private var detail: some View {
        if let s = store.selected {
            VStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(s.doc.title.isEmpty ? s.id : s.doc.title)
                        .font(.system(size: 20, weight: .bold))
                        .foregroundStyle(Theme.text)
                    Text(s.url.path)
                        .font(.system(size: 12, weight: .semibold, design: .monospaced))
                        .foregroundStyle(Theme.faint)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 22).padding(.top, 16).padding(.bottom, 12)
                Rectangle().fill(Color.white.opacity(0.07)).frame(height: 1)

                if store.reading {
                    readingView(s)
                } else {
                    timelineView(s)
                }
            }
        } else {
            VStack {
                Text("No sessions yet")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(Theme.muted)
                Text("Start one from the menu bar.")
                    .font(.system(size: 13)).foregroundStyle(Theme.faint)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func timelineView(_ s: LibrarySession) -> some View {
        ScrollView {
            VStack(spacing: 0) {
                VStack(spacing: 0) {
                    if !s.doc.shots.isEmpty {
                        ShotStrip(session: s, player: player)
                        Rectangle().fill(Color.white.opacity(0.07)).frame(height: 1)
                    }
                    PlaybackBar(session: s, player: player)
                }
                .background(Color(hex: 0x1A191F))

                LazyVStack(spacing: 0) {
                    ForEach(Array(s.doc.events.enumerated()), id: \.element.id) { _, ev in
                        timelineRow(s, ev)
                    }
                }
                .padding(.horizontal, 22).padding(.top, 14).padding(.bottom, 28)
            }
        }
    }

    private func timelineRow(_ s: LibrarySession, _ ev: SessionEvent) -> some View {
        let active = player.t >= ev.t
            && (s.doc.events.last(where: { $0.t <= player.t })?.id == ev.id)
        let inClip: Bool = {
            guard let (lo, hi) = player.clipRange else { return true }
            return ev.t >= lo && ev.t <= hi
        }()
        return HoverRow(radius: 9, base: active ? Theme.accent.opacity(0.12) : .clear,
                        hover: Color.white.opacity(0.05), action: {
            if ev.img != nil, let idx = s.doc.shots.firstIndex(where: { $0.id == ev.id }) {
                player.pause()
                player.seek(ev.t)
                store.previewIndex = idx
            } else if player.hasAudio {
                player.play(from: ev.t)
            } else {
                player.seek(ev.t)
            }
        }) {
            HStack(alignment: .top, spacing: 12) {
                Text(ev.stamp)
                    .font(.system(size: 11, weight: .semibold, design: .monospaced))
                    .foregroundStyle(active ? Theme.pink : Theme.faint)
                    .frame(width: 40, alignment: .leading).padding(.top, 2)
                Circle().fill(ev.kind.color).frame(width: 8, height: 8).padding(.top, 5)
                Text(highlightMatches(ev.text, query: store.search))
                    .font(.system(size: 14, design: ev.kind.mono ? .monospaced : .default))
                    .foregroundStyle(ev.kind.textColor)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if ev.img != nil {
                    Image(systemName: "photo")
                        .font(.system(size: 11)).foregroundStyle(Theme.faint)
                }
            }
            .padding(.horizontal, 9).padding(.vertical, 7)
        }
        .opacity(inClip ? 1 : 0.3)
        .onHover { store.hoverT = $0 ? ev.t : nil }
    }

    private func readingView(_ s: LibrarySession) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text(s.doc.title.isEmpty ? s.id : s.doc.title)
                    .font(.system(size: 26, weight: .bold))
                    .foregroundStyle(Theme.text)
                VStack(alignment: .leading, spacing: 3) {
                    if !s.doc.capture.isEmpty {
                        Text("capture: \(s.doc.capture)")
                    }
                    if !s.doc.host.isEmpty { Text("host: \(s.doc.host)") }
                    if !s.doc.displays.isEmpty { Text("displays: \(s.doc.displays)") }
                    Text("started \(s.doc.start) · \(s.durationLabel) · \(s.doc.shots.count) shots")
                }
                .font(.system(size: 13, design: .monospaced))
                .foregroundStyle(Theme.faint)
                Rectangle().fill(Theme.hairline).frame(height: 1)
                ForEach(s.doc.events) { ev in
                    readingRow(s, ev)
                }
            }
            .frame(maxWidth: 640)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 40).padding(.vertical, 28)
        }
    }

    // A reading row that carries a screenshot opens it in Quick Look.
    @ViewBuilder private func readingRow(_ s: LibrarySession, _ ev: SessionEvent)
        -> some View {
        let body = VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text("[\(ev.stamp)] \(String(describing: ev.kind))")
                    .font(.system(size: 11, weight: .semibold, design: .monospaced))
                    .foregroundStyle(Theme.faint)
                if let img = ev.img {
                    Image(systemName: "photo")
                        .font(.system(size: 10)).foregroundStyle(Theme.faint)
                    Text(img)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundStyle(Theme.faint)
                }
            }
            Text(highlightMatches(ev.text, query: store.search))
                .font(.system(size: 15, design: ev.kind.mono ? .monospaced : .default))
                .foregroundStyle(ev.kind.textColor)
                .lineSpacing(4)
        }
        if let img = ev.img {
            body
                .contentShape(Rectangle())
                .onTapGesture {
                    store.quickLook(s.url.appendingPathComponent(img))
                }
                .onHover { inside in
                    if inside { NSCursor.pointingHand.push() } else { NSCursor.pop() }
                }
                .help("Open \(img) in Quick Look")
        } else {
            body
        }
    }
}
