import SwiftUI

// The live session document, rendered from SESSION.live.md as the recorder
// rewrites it, with the portal state alongside.
struct LiveWindowView: View {
    @EnvironmentObject var recorder: Recorder
    @EnvironmentObject var settings: AppSettings

    var body: some View {
        HStack(spacing: 0) {
            docColumn
            Rectangle().fill(Theme.hairline).frame(width: 1)
            portalColumn
        }
        .background(Theme.bg.opacity(0.98))
        .frame(minWidth: 520, minHeight: 400)
    }

    private var docColumn: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(recorder.isRecording
                         ? "Untitled session · titling when you stop"
                         : (recorder.liveDoc.title.isEmpty ? "Session"
                            : recorder.liveDoc.title))
                        .font(.system(size: 16, weight: .bold))
                        .foregroundStyle(Theme.text)
                    if recorder.isRecording {
                        HStack(spacing: 6) {
                            Circle().fill(Theme.green).frame(width: 6, height: 6)
                            Text("rewriting")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(Theme.green)
                        }
                        .padding(.horizontal, 9).padding(.vertical, 3)
                        .background(Capsule().fill(Theme.green.opacity(0.14)))
                        .overlay(Capsule().stroke(Theme.green.opacity(0.3)))
                    }
                }
                Text(settings.outRootURL.path)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(Theme.faint)
            }
            .padding(.horizontal, 16).padding(.top, 14).padding(.bottom, 10)
            Rectangle().fill(Color.white.opacity(0.06)).frame(height: 1)

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 11) {
                        ForEach(recorder.liveDoc.events) { ev in
                            EventRow(event: ev)
                        }
                        HStack(spacing: 11) {
                            Color.clear.frame(width: 38, height: 1)
                            Circle().fill(Theme.faint).frame(width: 7, height: 7)
                            HStack(spacing: 3) {
                                Text("listening").font(.system(size: 13))
                                    .foregroundStyle(Theme.faint)
                                Caret()
                            }
                        }
                        .opacity(0.55)
                        .id("tail")
                    }
                    .padding(.horizontal, 16).padding(.vertical, 12)
                }
                .onChange(of: recorder.liveDoc.events.count) {
                    withAnimation { proxy.scrollTo("tail", anchor: .bottom) }
                }
            }
        }
        .frame(maxWidth: .infinity)
    }

    private var portalColumn: some View {
        VStack(alignment: .leading, spacing: 0) {
            if settings.portalActive {
                HStack(spacing: 8) {
                    Circle().fill(Theme.amber).frame(width: 8, height: 8)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Portal room open")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(Theme.text)
                        Text("the room can read the whole session library")
                            .font(.system(size: 11))
                            .foregroundStyle(Theme.faint)
                    }
                }
                .padding(14)
                Rectangle().fill(Color.white.opacity(0.06)).frame(height: 1)
                Spacer()
                VStack(alignment: .leading, spacing: 6) {
                    Text(recorder.activeRoom.isEmpty ? settings.portalURL
                         : recorder.activeRoom)
                        .font(.system(size: 11, weight: .semibold, design: .monospaced))
                        .foregroundStyle(Theme.amber)
                        .lineLimit(1)
                    Button("Copy room") {
                        let pb = NSPasteboard.general
                        pb.clearContents()
                        pb.setString(
                            recorder.activeRoom.isEmpty ? settings.portalURL
                                : recorder.activeRoom,
                            forType: .string)
                    }
                    .font(.system(size: 12, weight: .semibold))
                }
                .padding(14)
            } else {
                Spacer()
                VStack(spacing: 8) {
                    Text("Portal is off. Turn on **Share live with agent** in Settings → Agents & Portal to open a room.")
                        .font(.system(size: 13))
                        .foregroundStyle(Theme.muted)
                        .multilineTextAlignment(.center)
                }
                .padding(14)
                .overlay(
                    RoundedRectangle(cornerRadius: 13)
                        .stroke(style: StrokeStyle(lineWidth: 1, dash: [4]))
                        .foregroundStyle(Color.white.opacity(0.16)))
                .padding(14)
                Spacer()
            }
        }
        .frame(width: 216)
        .background(Color.black.opacity(0.22))
    }
}

struct EventRow: View {
    let event: SessionEvent
    var fontSize: CGFloat = 13

    var body: some View {
        HStack(alignment: .top, spacing: 11) {
            Text(event.stamp)
                .font(.system(size: 11, weight: .semibold, design: .monospaced)
                    .monospacedDigit())
                .foregroundStyle(Theme.faint)
                .frame(width: 38, alignment: .leading)
                .padding(.top, 2)
            Circle().fill(event.kind.color).frame(width: 7, height: 7).padding(.top, 5)
            Text(event.text)
                .font(.system(size: fontSize, design: event.kind.mono ? .monospaced : .default))
                .foregroundStyle(event.kind.textColor)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}
