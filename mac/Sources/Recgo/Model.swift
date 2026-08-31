import Foundation
import SwiftUI
import AVFoundation

// The SESSION.md / SESSION.live.md line grammar, ported from
// cmd/recgo-sessions/main.go. Both documents share it by construction.
enum EventKind: String {
    case narration, click, mark, focus, window, shot, error, network, console, sys

    var color: Color {
        switch self {
        case .narration: return Theme.accent
        case .click: return Theme.clickBlue
        case .mark: return Theme.amber
        case .focus, .window: return Theme.focusPurple
        case .shot: return Theme.shotGreen
        case .error: return Theme.pink
        case .network: return Theme.netOrange
        case .console: return Theme.muted
        case .sys: return Theme.faint
        }
    }

    var textColor: Color {
        switch self {
        case .narration: return Theme.text
        case .click, .focus, .window: return Theme.secondary
        case .mark: return Theme.amber
        case .error: return Theme.pink
        case .network: return Theme.netText
        case .shot, .console, .sys: return Theme.muted
        }
    }

    var mono: Bool {
        switch self {
        case .narration, .mark: return false
        default: return true
        }
    }
}

struct SessionEvent: Identifiable {
    let id = UUID()
    let t: Double
    let kind: EventKind
    let text: String
    let img: String?
    let x: Int?
    let y: Int?

    var stamp: String { clockString(t) }
}

struct SessionDoc {
    var title = ""
    var start = ""
    var folder = ""
    var page = ""
    var capture = ""
    var host = ""
    var displays = ""
    var tool = ""
    var durationSec = 0
    var initialShot: String?
    var notes: [String] = []
    var events: [SessionEvent] = []

    var shots: [SessionEvent] { events.filter { $0.img != nil } }
    var marks: Int { events.filter { $0.kind == .mark }.count }
    var errors: Int { events.filter { $0.kind == .error }.count }
}

func clockString(_ sec: Double) -> String {
    let s = max(0, Int(sec.rounded()))
    return String(format: "%02d:%02d", s / 60, s % 60)
}

private let clickRE = try! NSRegularExpression(
    pattern: #"^(\d\d\.\d\d\.\d\d)  Click: (.*?) → (\d{4}\.png)( — screen did not repaint)?\s*$"#)
private let shotRE = try! NSRegularExpression(
    pattern: #"^(\d\d\.\d\d\.\d\d)  (Mark|Focus|Window): (.*?)(?: → (\d{4}\.png))?\s*$"#)
private let narrRE = try! NSRegularExpression(
    pattern: #"^(\d\d\.\d\d\.\d\d)  \*\*user narration\*\*: (.*)$"#)
private let otherRE = try! NSRegularExpression(
    pattern: #"^(\d\d\.\d\d\.\d\d)  ([A-Za-z._]+): (.*)$"#)
private let coordRE = try! NSRegularExpression(
    pattern: #"^(\d+),(\d+)(?: on (.*))?$"#, options: [.dotMatchesLineSeparators])
private let durationRE = try! NSRegularExpression(pattern: #"Recorded (\d+)s"#)

private func firstMatch(_ re: NSRegularExpression, _ line: String) -> [String]? {
    let ns = line as NSString
    guard let m = re.firstMatch(in: line, range: NSRange(location: 0, length: ns.length)) else {
        return nil
    }
    return (0..<m.numberOfRanges).map {
        let r = m.range(at: $0)
        return r.location == NSNotFound ? "" : ns.substring(with: r)
    }
}

private func clockSeconds(_ s: String) -> Double {
    let p = s.split(separator: ".").compactMap { Double($0) }
    guard p.count == 3 else { return 0 }
    return p[0] * 3600 + p[1] * 60 + p[2]
}

func parseSessionDoc(_ text: String) -> SessionDoc {
    var doc = SessionDoc()
    for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
        let line = String(raw)
        if line.hasPrefix("# Session: ") {
            doc.title = String(line.dropFirst("# Session: ".count))
            continue
        }
        if line.hasPrefix("Start: ") { doc.start = String(line.dropFirst(7)); continue }
        if line.hasPrefix("Folder: ") { doc.folder = String(line.dropFirst(8)); continue }
        if line.hasPrefix("Page: ") { doc.page = String(line.dropFirst(6)); continue }
        if line.hasPrefix("Capture: ") { doc.capture = String(line.dropFirst(9)); continue }
        if line.hasPrefix("Host: ") { doc.host = String(line.dropFirst(6)); continue }
        if line.hasPrefix("Displays: ") { doc.displays = String(line.dropFirst(10)); continue }
        if line.hasPrefix("Initial screenshot: ") {
            doc.initialShot = String(line.dropFirst("Initial screenshot: ".count))
            doc.events.append(SessionEvent(
                t: 0, kind: .shot, text: "initial screenshot", img: doc.initialShot,
                x: nil, y: nil))
            continue
        }
        if line.hasPrefix("Recorded ") {
            if let m = firstMatch(durationRE, line), let d = Int(m[1]) { doc.durationSec = d }
            if let r = line.range(of: "by recgo") {
                doc.tool = line[r.lowerBound...].dropFirst(3)
                    .split(separator: " ").first.map(String.init) ?? ""
            }
            doc.notes.append(line)
            continue
        }

        if let m = firstMatch(clickRE, line) {
            var x: Int?, y: Int?
            var what = m[2]
            if let c = firstMatch(coordRE, m[2]) {
                x = Int(c[1]); y = Int(c[2])
                what = c[3].isEmpty ? "\(c[1]),\(c[2])" : "\(c[1]),\(c[2]) on \(c[3])"
            }
            var text = what
            if !m[4].isEmpty { text += " — screen did not repaint" }
            doc.events.append(SessionEvent(
                t: clockSeconds(m[1]), kind: .click, text: text, img: m[3], x: x, y: y))
            continue
        }
        if let m = firstMatch(shotRE, line) {
            let kind: EventKind = m[2] == "Mark" ? .mark : (m[2] == "Focus" ? .focus : .window)
            let text = kind == .mark ? "mark \(m[3])" : m[3]
            doc.events.append(SessionEvent(
                t: clockSeconds(m[1]), kind: kind, text: text,
                img: m[4].isEmpty ? nil : m[4], x: nil, y: nil))
            continue
        }
        if let m = firstMatch(narrRE, line) {
            doc.events.append(SessionEvent(
                t: clockSeconds(m[1]), kind: .narration, text: m[2], img: nil, x: nil, y: nil))
            continue
        }
        if let m = firstMatch(otherRE, line) {
            let label = m[2]
            let kind: EventKind
            var text = m[3]
            switch true {
            case label == "Error" && text.hasPrefix("network"):
                kind = .network
            case label == "Error":
                kind = .error
            case label.hasPrefix("console."):
                kind = label.hasSuffix("error") ? .error : .console
                text = "\(label): \(text)"
            case label == "Navigate", label == "Tab", label == "HMR":
                kind = .sys
                text = "\(label): \(text)"
            default:
                kind = .sys
                text = "\(label): \(text)"
            }
            doc.events.append(SessionEvent(
                t: clockSeconds(m[1]), kind: kind, text: text, img: nil, x: nil, y: nil))
            continue
        }
    }
    return doc
}

// One finished session folder in the library.
struct LibrarySession: Identifiable, Equatable {
    let id: String
    let url: URL
    let doc: SessionDoc
    let modified: Date

    static func == (a: LibrarySession, b: LibrarySession) -> Bool { a.id == b.id }

    var durationLabel: String { clockString(Double(doc.durationSec)) }
    var meta: String {
        var parts: [String] = []
        let start = doc.start
        if start.count >= 16 { parts.append(String(start.dropFirst(11).prefix(5))) }
        parts.append(durationLabel)
        if !doc.tool.isEmpty { parts.append(doc.tool.replacingOccurrences(of: "recgo-", with: "")) }
        parts.append("\(doc.shots.count) shots")
        if doc.errors > 0 { parts.append("\(doc.errors) error\(doc.errors == 1 ? "" : "s")") }
        if doc.marks > 0 { parts.append("\(doc.marks) mark\(doc.marks == 1 ? "" : "s")") }
        return parts.joined(separator: " · ")
    }
}

func scanLibrary(root: URL) -> [LibrarySession] {
    let fm = FileManager.default
    guard let entries = try? fm.contentsOfDirectory(
        at: root, includingPropertiesForKeys: [.isDirectoryKey, .contentModificationDateKey])
    else { return [] }

    var out: [LibrarySession] = []
    for dir in entries {
        guard (try? dir.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory == true else {
            continue
        }
        if dir.lastPathComponent.contains("-recording-") { continue }
        let mdURL = dir.appendingPathComponent("SESSION.md")
        guard let text = try? String(contentsOf: mdURL, encoding: .utf8) else { continue }
        let mod = (try? mdURL.resourceValues(forKeys: [.contentModificationDateKey]))?
            .contentModificationDate ?? .distantPast
        out.append(LibrarySession(
            id: dir.lastPathComponent, url: dir, doc: parseSessionDoc(text), modified: mod))
    }
    return out.sorted { $0.modified > $1.modified }
}

// Waveform: RMS-binned amplitude of audio.wav, matching the demo's 46 bars.
func waveformBins(url: URL, bins: Int = 46) -> [Double] {
    guard let file = try? AVAudioFile(forReading: url) else { return [] }
    let frames = AVAudioFrameCount(file.length)
    guard frames > 0,
          let buf = AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: frames),
          (try? file.read(into: buf)) != nil,
          let data = buf.floatChannelData
    else { return [] }

    let n = Int(buf.frameLength)
    let ch = data[0]
    var out = [Double](repeating: 0, count: bins)
    let per = max(1, n / bins)
    for b in 0..<bins {
        let lo = b * per
        let hi = min(n, lo + per)
        if lo >= hi { break }
        var acc = 0.0
        var i = lo
        // Sample sparsely: full RMS over an hour of audio is wasted work here.
        let stride = max(1, (hi - lo) / 500)
        var count = 0
        while i < hi {
            let v = Double(ch[i])
            acc += v * v
            count += 1
            i += stride
        }
        out[b] = count > 0 ? (acc / Double(count)).squareRoot() : 0
    }
    let peak = out.max() ?? 0
    if peak > 0 { out = out.map { $0 / peak } }
    return out
}
