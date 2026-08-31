import SwiftUI

// Palette lifted from the walk-and-talk demo (Recgo for Mac.dc.html).
enum Theme {
    static let bg = Color(hex: 0x161518)
    static let surface = Color(hex: 0x201E25)
    static let card = Color.black.opacity(0.34)
    static let accent = Color(hex: 0xFF2D55)
    static let accentHover = Color(hex: 0xFF4B6E)
    static let pink = Color(hex: 0xFF859C)
    static let amber = Color(hex: 0xFFBC5B)
    static let green = Color(hex: 0x30CD00)
    static let text = Color(hex: 0xFCFCFC)
    static let secondary = Color(hex: 0xCFCDD4)
    static let muted = Color(hex: 0xA09BA8)
    static let faint = Color(hex: 0x716B7C)
    static let clickBlue = Color(hex: 0xA0ABFF)
    static let focusPurple = Color(hex: 0xC640CD)
    static let shotGreen = Color(hex: 0x34CE76)
    static let netOrange = Color(hex: 0xFF7439)
    static let netText = Color(hex: 0xFF9E7A)
    static let hairline = Color.white.opacity(0.10)
    static let rowHover = Color.white.opacity(0.10)
    static let control = Color.white.opacity(0.08)

    static let mono = Font.system(size: 12, design: .monospaced)
}

extension Color {
    init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: 1
        )
    }
}

// A row that highlights on hover the way every popover row in the demo does.
struct HoverRow<Content: View>: View {
    var radius: CGFloat = 11
    var base: Color = .clear
    var hover: Color = Theme.rowHover
    let action: () -> Void
    @ViewBuilder let content: () -> Content
    @State private var over = false

    var body: some View {
        Button(action: action) {
            content()
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(RoundedRectangle(cornerRadius: radius).fill(over ? hover : base))
        .onHover { over = $0 }
    }
}

struct GlassBackground: NSViewRepresentable {
    var material: NSVisualEffectView.Material = .hudWindow
    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = material
        v.state = .active
        v.blendingMode = .behindWindow
        v.appearance = NSAppearance(named: .darkAqua)
        return v
    }
    func updateNSView(_ nsView: NSVisualEffectView, context: Context) {}
}
