import SwiftUI

struct ShotStrip: View {
    let session: LibrarySession
    @ObservedObject var player: Player
    @ObservedObject var store = LibraryStore.shared

    // The shot the pointer's timeline position is nearest to: the strip
    // drifts there and rings it softly, without seeking anything.
    private var softID: UUID? {
        guard let t = store.hoverT else { return nil }
        return session.doc.shots.min { abs($0.t - t) < abs($1.t - t) }?.id
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal) {
                HStack(spacing: 8) {
                    ForEach(Array(session.doc.shots.enumerated()), id: \.element.id) { i, ev in
                        shotThumb(i, ev).id(ev.id)
                    }
                }
                .padding(.horizontal, 22).padding(.vertical, 10)
            }
            .onChange(of: softID) { _, target in
                guard let target else { return }
                withAnimation(.easeOut(duration: 0.3)) {
                    proxy.scrollTo(target, anchor: .center)
                }
            }
        }
    }

    private func shotThumb(_ i: Int, _ ev: SessionEvent) -> some View {
        let inClip: Bool = {
            guard let (lo, hi) = player.clipRange else { return true }
            return ev.t >= lo && ev.t <= hi
        }()
        let isClick = ev.kind == .click
        return Button {
            player.pause()
            player.seek(ev.t)
            store.previewIndex = i
        } label: {
            VStack(alignment: .leading, spacing: 5) {
                ZStack(alignment: .bottomLeading) {
                    ShotImage(url: session.url.appendingPathComponent(ev.img ?? ""))
                        .frame(width: 118, height: 70)
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                    Text(ev.stamp)
                        .font(.system(size: 9, weight: .bold).monospacedDigit())
                        .foregroundStyle(Theme.text)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(RoundedRectangle(cornerRadius: 4)
                            .fill(Color.black.opacity(0.62)))
                        .padding(5)
                }
                .overlay(RoundedRectangle(cornerRadius: 8)
                    .stroke(softID == ev.id ? Theme.amber.opacity(0.7)
                            : (isClick ? Theme.accent.opacity(0.45)
                               : Color.white.opacity(0.10)),
                            lineWidth: softID == ev.id ? 1.5 : 1))
                Text(label(ev))
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(isClick ? Theme.pink : Theme.faint)
                    .lineLimit(1)
                    .frame(width: 118, alignment: .leading)
            }
        }
        .buttonStyle(.plain)
        .opacity(inClip ? 1 : 0.32)
    }

    private func label(_ ev: SessionEvent) -> String {
        switch ev.kind {
        case .click: return "click · " + ev.text.prefix(28)
        case .mark: return "mark"
        case .focus: return "focus · " + ev.text.prefix(24)
        case .window: return "window · " + ev.text.prefix(22)
        default: return ev.text
        }
    }
}

struct ShotImage: View {
    let url: URL
    @State private var image: NSImage?

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image).resizable().aspectRatio(contentMode: .fill)
            } else {
                LinearGradient(
                    colors: [Color(hex: 0x2A2731), Color(hex: 0x1B1A1F)],
                    startPoint: .topLeading, endPoint: .bottomTrailing)
            }
        }
        .task(id: url) {
            let u = url
            let img = await Task.detached(priority: .utility) { () -> NSImage? in
                guard let img = NSImage(contentsOf: u) else { return nil }
                return img
            }.value
            image = img
        }
    }
}

struct PlaybackBar: View {
    let session: LibrarySession
    @ObservedObject var player: Player
    @ObservedObject var store = LibraryStore.shared

    var body: some View {
        VStack(spacing: 10) {
            HStack(spacing: 12) {
                Button { player.toggle() } label: {
                    Image(systemName: player.playing ? "pause.fill" : "play.fill")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundStyle(Theme.text)
                        .frame(width: 34, height: 34)
                        .background(Circle().fill(player.hasAudio ? Theme.accent : Theme.faint))
                        .shadow(color: Theme.accent.opacity(player.hasAudio ? 0.42 : 0),
                                radius: 10)
                }
                .buttonStyle(.plain)
                .disabled(!player.hasAudio)

                Text("\(clockString(player.t)) / \(clockString(player.duration))")
                    .font(.system(size: 13, weight: .bold).monospacedDigit())
                    .foregroundStyle(Theme.text)
                    .frame(width: 96, alignment: .leading)

                smallButton(player.rate == 1 ? "1×" : (player.rate == 1.5 ? "1.5×" : "2×")) {
                    player.cycleRate()
                }
                Spacer()
                smallButton("Set start") {
                    player.clipIn = player.t
                    player.clipOut = nil
                }
                smallButton("Set end") {
                    if player.clipIn == nil { player.clipIn = 0 }
                    player.clipOut = player.t
                }
            }

            waveform

            HStack(spacing: 8) {
                Text(clipHint)
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(clipHintColor)
                Spacer()
                if player.clipRange != nil {
                    smallButton("Play clip") { player.playClip() }
                    Button {
                        player.exportClip(session: session) { store.showToast($0) }
                    } label: {
                        Text("Export clip").font(.system(size: 12, weight: .bold))
                            .foregroundStyle(Theme.text)
                            .padding(.horizontal, 11).padding(.vertical, 5)
                            .background(RoundedRectangle(cornerRadius: 8).fill(Theme.accent))
                    }
                    .buttonStyle(.plain)
                    smallButton("Clear") {
                        player.clipIn = nil
                        player.clipOut = nil
                    }
                }
            }
            .frame(minHeight: 26)
        }
        .padding(.horizontal, 22).padding(.top, 12).padding(.bottom, 14)
        .background(Color.black.opacity(0.20))
    }

    private var waveform: some View {
        GeometryReader { geo in
            let bins = player.bins
            let total = max(player.duration, 1)
            ZStack(alignment: .leading) {
                HStack(alignment: .bottom, spacing: 2) {
                    if bins.isEmpty {
                        Text(player.hasAudio ? "reading waveform…" : "no audio track")
                            .font(.system(size: 11)).foregroundStyle(Theme.faint)
                            .frame(maxWidth: .infinity)
                    } else {
                        ForEach(0..<bins.count, id: \.self) { i in
                            let sec = (Double(i) + 0.5) / Double(bins.count) * total
                            RoundedRectangle(cornerRadius: 1.5)
                                .fill(sec <= player.t ? Theme.accent
                                      : Color.white.opacity(0.20))
                                .frame(height: max(3, bins[i] * 44))
                                .frame(maxWidth: .infinity)
                                .onTapGesture {
                                    if player.hasAudio { player.play(from: sec) }
                                    else { player.seek(sec) }
                                }
                        }
                    }
                }
                .frame(height: 46, alignment: .bottom)

                if let (lo, hi) = player.clipRange {
                    RoundedRectangle(cornerRadius: 6)
                        .fill(Theme.accent.opacity(0.14))
                        .overlay(RoundedRectangle(cornerRadius: 6)
                            .stroke(Theme.accent, lineWidth: 1))
                        .frame(width: max(4, (hi - lo) / total * geo.size.width))
                        .offset(x: lo / total * geo.size.width)
                        .allowsHitTesting(false)
                }
                if let ht = store.hoverT {
                    Rectangle().fill(Theme.amber.opacity(0.55))
                        .frame(width: 1.5)
                        .offset(x: min(ht / total, 1) * max(geo.size.width - 2, 0))
                        .allowsHitTesting(false)
                }
                Rectangle().fill(Color.white)
                    .frame(width: 2)
                    .shadow(color: .white.opacity(0.7), radius: 5)
                    .offset(x: min(player.t / total, 1) * max(geo.size.width - 2, 0))
                    .allowsHitTesting(false)
            }
        }
        .frame(height: 46)
    }

    private var clipHint: String {
        if let (lo, hi) = player.clipRange {
            return "Clip \(clockString(lo)) – \(clockString(hi)) · \(clockString(hi - lo)) long"
        }
        if let lo = player.clipIn {
            return "Start at \(clockString(lo)) — now set the end"
        }
        return "Play back, then Set start / Set end to slice a clip"
    }

    private var clipHintColor: Color {
        if player.clipRange != nil { return Theme.text }
        if player.clipIn != nil { return Theme.amber }
        return Theme.faint
    }

    private func smallButton(_ label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(label).font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.secondary)
                .padding(.horizontal, 11).padding(.vertical, 5)
                .background(RoundedRectangle(cornerRadius: 8).fill(Theme.control))
        }
        .buttonStyle(.plain)
    }
}

struct ShotPreview: View {
    let session: LibrarySession
    let index: Int
    @ObservedObject var store = LibraryStore.shared

    private var shots: [SessionEvent] { session.doc.shots }
    private var shot: SessionEvent? {
        index >= 0 && index < shots.count ? shots[index] : nil
    }

    var body: some View {
        ZStack {
            Color(hex: 0x161518).opacity(0.78)
                .onTapGesture { store.previewIndex = nil }
            if let shot {
                VStack(spacing: 12) {
                    HStack(spacing: 12) {
                        Text(shot.kind == .click ? "click" : String(describing: shot.kind))
                            .font(.system(size: 17, weight: .bold))
                            .foregroundStyle(Theme.text)
                        Text(shot.stamp)
                            .font(.system(size: 13, weight: .semibold, design: .monospaced))
                            .foregroundStyle(Theme.faint)
                        Spacer()
                        Text("\(index + 1) of \(shots.count)")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.faint)
                        navButton("chevron.left") {
                            store.previewIndex = (index + shots.count - 1) % shots.count
                        }
                        navButton("chevron.right") {
                            store.previewIndex = (index + 1) % shots.count
                        }
                        navButton("xmark") { store.previewIndex = nil }
                    }
                    ShotImage(url: session.url.appendingPathComponent(shot.img ?? ""))
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                        .overlay(RoundedRectangle(cornerRadius: 12)
                            .stroke(Color.white.opacity(0.14)))
                        .shadow(color: .black.opacity(0.6), radius: 30)
                    HStack(spacing: 10) {
                        Text(session.url.appendingPathComponent(shot.img ?? "").path)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(Theme.muted)
                            .lineLimit(1)
                        Spacer()
                        previewButton("Copy image") {
                            let url = session.url.appendingPathComponent(shot.img ?? "")
                            if let img = NSImage(contentsOf: url) {
                                let pb = NSPasteboard.general
                                pb.clearContents()
                                pb.writeObjects([img])
                                store.showToast("Image copied")
                            }
                        }
                        previewButton("Reveal in Finder") {
                            NSWorkspace.shared.activateFileViewerSelecting(
                                [session.url.appendingPathComponent(shot.img ?? "")])
                        }
                    }
                }
                .padding(24)
                .frame(maxWidth: 860)
            }
        }
    }

    private func navButton(_ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.secondary)
                .frame(width: 30, height: 30)
                .background(RoundedRectangle(cornerRadius: 9).fill(Theme.control))
        }
        .buttonStyle(.plain)
    }

    private func previewButton(_ label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(label).font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.secondary)
                .padding(.horizontal, 13).padding(.vertical, 7)
                .background(RoundedRectangle(cornerRadius: 10).fill(Theme.control))
        }
        .buttonStyle(.plain)
    }
}
