// Draws AppIcon.iconset for Recgo.app — build.sh runs `swift icon.swift .build`
// and feeds the result to iconutil, so the icon is versioned as code and no
// binary artwork lives in the repo. The artwork is the menu bar's record.circle
// motif on Apple's Big Sur icon grid (824pt squircle on a 1024pt canvas), in
// the Theme.swift palette.
import AppKit

let outRoot = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
let iconset = URL(fileURLWithPath: outRoot).appendingPathComponent("AppIcon.iconset")
try FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)

func srgb(_ hex: UInt32, _ alpha: CGFloat = 1) -> NSColor {
    NSColor(
        srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
        green: CGFloat((hex >> 8) & 0xFF) / 255,
        blue: CGFloat(hex & 0xFF) / 255,
        alpha: alpha)
}

func draw(px: Int) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .calibratedRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    defer { NSGraphicsContext.restoreGraphicsState() }

    let k = CGFloat(px) / 1024
    let plate = NSRect(x: 100 * k, y: 100 * k, width: 824 * k, height: 824 * k)
    let squircle = NSBezierPath(roundedRect: plate, xRadius: 186 * k, yRadius: 186 * k)
    NSGradient(starting: srgb(0x2A2731), ending: srgb(0x131216))!
        .draw(in: squircle, angle: -90)

    squircle.addClip()

    let center = NSPoint(x: 512 * k, y: 512 * k)
    func oval(radius: CGFloat) -> NSBezierPath {
        NSBezierPath(ovalIn: NSRect(
            x: center.x - radius, y: center.y - radius,
            width: radius * 2, height: radius * 2))
    }

    let glow = NSShadow()
    glow.shadowColor = srgb(0xFF2D55, 0.55)
    glow.shadowBlurRadius = 44 * k
    glow.set()

    srgb(0xFF2D55).setStroke()
    let ring = oval(radius: 244 * k)
    ring.lineWidth = 62 * k
    ring.stroke()

    srgb(0xFF2D55).setFill()
    oval(radius: 122 * k).fill()

    NSShadow().set()
    srgb(0xFFFFFF, 0.07).setStroke()
    let hairline = NSBezierPath(
        roundedRect: plate.insetBy(dx: 3 * k, dy: 3 * k),
        xRadius: 183 * k, yRadius: 183 * k)
    hairline.lineWidth = 6 * k
    hairline.stroke()

    return rep
}

let sizes = [(16, 1), (16, 2), (32, 1), (32, 2), (128, 1), (128, 2),
             (256, 1), (256, 2), (512, 1), (512, 2)]
for (base, scale) in sizes {
    let rep = draw(px: base * scale)
    rep.size = NSSize(width: base, height: base)
    let name = scale == 1 ? "icon_\(base)x\(base).png" : "icon_\(base)x\(base)@2x.png"
    try rep.representation(using: .png, properties: [:])!
        .write(to: iconset.appendingPathComponent(name))
}
print("wrote \(iconset.path)")
