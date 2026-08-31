import SwiftUI

struct HUDView: View {
    @EnvironmentObject var recorder: Recorder
    @EnvironmentObject var settings: AppSettings
    @State private var collapsed = false

    private var finishing: Bool { recorder.phase == .finishing }

    var body: some View {
        Group {
            if collapsed { pill } else { panel }
        }
        .animation(.spring(duration: 0.26), value: collapsed)
    }

    private var pill: some View {
        Button { collapsed = false } label: {
            HStack(spacing: 10) {
                if finishing {
                    ProgressView().controlSize(.small)
                    Text("Finishing…")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(Theme.text)
                } else {
                    RecDot()
                    Text(clockString(recorder.elapsed))
                        .font(.system(size: 14, weight: .bold).monospacedDigit())
                        .foregroundStyle(Theme.text)
                }
            }
            .padding(.horizontal, 15).padding(.vertical, 10)
            .background(Capsule().fill(Theme.surface.opacity(0.94)))
            .overlay(Capsule().stroke(Color.white.opacity(0.12)))
        }
        .buttonStyle(.plain)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
        .padding(2)
    }

    private var panel: some View {
        VStack(spacing: 12) {
            // Drag handle row: expand-to-live-window and hide controls.
            HStack {
                Spacer()
                Capsule().fill(Color.white.opacity(0.18)).frame(width: 34, height: 3)
                Spacer()
                Button { Windows.shared.showLiveWindow() } label: {
                    Image(systemName: "sidebar.squares.left")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.secondary)
                }
                .buttonStyle(.plain)
                .help("Open the live session window")
                Button { collapsed = true } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.faint)
                }
                .buttonStyle(.plain)
                .help("Collapse the HUD")
            }
            .frame(height: 16)

            HStack(spacing: 12) {
                if finishing {
                    ProgressView().controlSize(.small)
                } else {
                    RecDot()
                }
                Text(clockString(recorder.elapsed))
                    .font(.system(size: 21, weight: .bold).monospacedDigit())
                    .foregroundStyle(finishing ? Theme.secondary : Theme.text)
                if finishing {
                    Text("Finishing")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Theme.amber)
                }
                Spacer()
                modeSegment
            }

            if finishing {
                finishingCard
            }

            if !finishing, recorder.mode == .audio || settings.captureMic {
                meterRow(
                    name: settings.micDevice.isEmpty ? "Microphone" : settings.micDevice)
            }

            if !finishing, settings.liveNarrationHUD {
                HStack(spacing: 0) {
                    Text(recorder.lastNarration.isEmpty
                         ? "waiting for speech…" : recorder.lastNarration)
                        .font(.system(size: 14))
                        .foregroundStyle(
                            recorder.lastNarration.isEmpty ? Theme.muted : Color(hex: 0xECEBED))
                        .lineLimit(2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Caret()
                }
                .padding(.horizontal, 13).padding(.vertical, 11)
                .frame(minHeight: 50)
                .background(
                    RoundedRectangle(cornerRadius: 13).fill(Theme.card)
                        .overlay(RoundedRectangle(cornerRadius: 13)
                            .stroke(Color.white.opacity(0.07))))
            }

            if !finishing, settings.portalActive {
                HStack(spacing: 9) {
                    Circle().fill(Theme.amber).frame(width: 8, height: 8)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Portal open")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.text)
                        Text(recorder.activeRoom.isEmpty ? settings.portalURL
                             : recorder.activeRoom)
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(Theme.amber)
                            .lineLimit(1)
                    }
                    Spacer()
                }
                .padding(.horizontal, 11).padding(.vertical, 7)
                .background(
                    RoundedRectangle(cornerRadius: 11).fill(Theme.amber.opacity(0.12))
                        .overlay(RoundedRectangle(cornerRadius: 11)
                            .stroke(Theme.amber.opacity(0.32))))
            }

            if finishing {
                if recorder.finishingElapsed > 8 {
                    Button { recorder.forceStop() } label: {
                        Text("Force stop — skip packing, keep raw files")
                            .font(.system(size: 13, weight: .bold))
                            .foregroundStyle(Theme.pink)
                            .frame(maxWidth: .infinity).frame(height: 38)
                            .background(
                                RoundedRectangle(cornerRadius: 12)
                                    .fill(Theme.accent.opacity(0.16))
                                    .overlay(RoundedRectangle(cornerRadius: 12)
                                        .stroke(Theme.accent.opacity(0.5))))
                    }
                    .buttonStyle(.plain)
                }
            } else {
                HStack(spacing: 8) {
                    hudButton {
                        HStack(spacing: 7) {
                            RoundedRectangle(cornerRadius: 2).fill(Theme.amber)
                                .frame(width: 10, height: 10).rotationEffect(.degrees(45))
                            Text("Mark").font(.system(size: 13, weight: .semibold))
                                .foregroundStyle(Theme.text)
                        }
                    } action: { Actions.mark() }
                    Button { Actions.stop() } label: {
                        HStack(spacing: 7) {
                            RoundedRectangle(cornerRadius: 2).fill(Theme.text)
                                .frame(width: 10, height: 10)
                            Text("Stop").font(.system(size: 13, weight: .bold))
                                .foregroundStyle(Theme.text)
                        }
                        .frame(maxWidth: .infinity).frame(height: 38)
                        .background(RoundedRectangle(cornerRadius: 12).fill(Theme.accent))
                        .shadow(color: Theme.accent.opacity(0.45), radius: 12)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(.horizontal, 16).padding(.top, 6).padding(.bottom, 14)
        .frame(width: 412)
        .background(
            RoundedRectangle(cornerRadius: 20).fill(Theme.surface.opacity(0.95))
                .overlay(RoundedRectangle(cornerRadius: 20)
                    .stroke(Color.white.opacity(0.12))))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
        .padding(2)
    }

    private var finishingCard: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Capture stopped — packing the session")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.text)
                Text(recorder.finishingStatus.isEmpty
                     ? "stopping…" : recorder.finishingStatus)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(Theme.muted)
                    .lineLimit(1)
            }
            Spacer()
            Text(clockString(recorder.finishingElapsed))
                .font(.system(size: 12, weight: .semibold).monospacedDigit())
                .foregroundStyle(Theme.amber)
        }
        .padding(.horizontal, 13).padding(.vertical, 10)
        .background(
            RoundedRectangle(cornerRadius: 13).fill(Theme.card)
                .overlay(RoundedRectangle(cornerRadius: 13)
                    .stroke(Theme.amber.opacity(0.25))))
    }

    // One engine per session: the segment shows where you are, and the other
    // modes are disabled with an explanation rather than pretending to switch.
    private var modeSegment: some View {
        HStack(spacing: 2) {
            ForEach(RecordMode.allCases) { m in
                Text(m == .tab ? "Tab" : m.label.replacingOccurrences(of: " only", with: ""))
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(m == recorder.mode ? Theme.text : Theme.muted.opacity(0.5))
                    .padding(.horizontal, 9).padding(.vertical, 4)
                    .background(
                        RoundedRectangle(cornerRadius: 8)
                            .fill(m == recorder.mode ? Theme.accent : .clear))
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color.black.opacity(0.3)))
        .help("One engine per session — stop and start again to record something else")
    }

    private func meterRow(name: String) -> some View {
        HStack(spacing: 10) {
            Text(name)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.secondary)
                .lineLimit(1)
                .frame(width: 124, alignment: .leading)
            LevelMeter()
        }
    }

    private func hudButton<Label: View>(
        @ViewBuilder label: () -> Label, action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            label()
                .frame(maxWidth: .infinity).frame(height: 38)
                .background(RoundedRectangle(cornerRadius: 12).fill(Theme.control))
        }
        .buttonStyle(.plain)
    }
}

struct LevelMeter: View {
    @State private var on = false
    private let heights: [CGFloat] = [0.4, 0.9, 0.55, 0.75, 1.0, 0.6, 0.85, 0.5, 0.95,
                                      0.7, 0.45, 0.8]

    var body: some View {
        HStack(alignment: .bottom, spacing: 2) {
            ForEach(0..<heights.count, id: \.self) { i in
                RoundedRectangle(cornerRadius: 1.5)
                    .fill(i < 4 ? Theme.accent : (i < 8 ? Color(hex: 0xFF7C77) : Theme.amber))
                    .frame(height: 16)
                    .scaleEffect(y: on ? heights[i] : 0.16, anchor: .bottom)
                    .animation(
                        .easeInOut(duration: 0.6 + Double(i % 5) * 0.08)
                            .repeatForever()
                            .delay(Double(i) * 0.05),
                        value: on)
            }
        }
        .frame(maxWidth: .infinity)
        .onAppear { on = true }
    }
}

struct Caret: View {
    @State private var visible = true

    var body: some View {
        Rectangle().fill(Theme.accent).frame(width: 2, height: 14)
            .opacity(visible ? 1 : 0)
            .onAppear {
                Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { _ in
                    visible.toggle()
                }
            }
    }
}
