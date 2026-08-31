import AppKit
import SwiftUI

// One owner for every window the app can show. All panels are dark-appearance
// and the recording surfaces are excluded from capture (sharingType = .none)
// so the HUD never appears in its own screenshots.
final class Windows {
    static let shared = Windows()

    private var hud: NSPanel?
    private var live: NSWindow?
    private var library: NSWindow?
    private var settings: NSWindow?

    private init() {
        // Close buttons and ⌘W go through performClose, which never touches
        // this class — so the Dock hand-back rides the notification.
        NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification, object: nil, queue: .main
        ) { [weak self] note in
            guard let self, let w = note.object as? NSWindow,
                  w === self.library || w === self.settings || w === self.live
            else { return }
            // isVisible is still true while the notification fires.
            DispatchQueue.main.async { self.refreshActivationPolicy() }
        }
    }

    // The Dock icon follows the real windows: Recgo stays a menu-bar
    // accessory until the library, settings, or live session window is up,
    // then takes a regular Dock/⌘Tab presence, and gives it back when the
    // last one closes. Never activates here — showLibrary/showSettings do
    // that themselves, and the live window must not steal focus from
    // whatever is being recorded.
    private func refreshActivationPolicy() {
        let visible = [library, settings, live].contains { $0?.isVisible == true }
        let want: NSApplication.ActivationPolicy = visible ? .regular : .accessory
        if NSApp.activationPolicy() != want {
            NSApp.setActivationPolicy(want)
        }
    }

    // MARK: HUD

    func showHUD() {
        if hud == nil {
            let panel = NSPanel(
                contentRect: NSRect(x: 0, y: 0, width: 412, height: 240),
                styleMask: [.borderless, .nonactivatingPanel, .fullSizeContentView],
                backing: .buffered, defer: false)
            panel.level = .floating
            panel.backgroundColor = .clear
            panel.isOpaque = false
            panel.hasShadow = true
            panel.sharingType = .none
            panel.isMovableByWindowBackground = true
            panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            panel.contentView = NSHostingView(
                rootView: HUDView().environmentObject(Recorder.shared)
                    .environmentObject(AppSettings.shared))
            if let screen = NSScreen.main {
                let f = screen.visibleFrame
                panel.setFrameOrigin(NSPoint(x: f.maxX - 448, y: f.minY + 82))
            }
            hud = panel
        }
        hud?.orderFrontRegardless()
    }

    func closeHUD() {
        hud?.orderOut(nil)
        hud = nil
    }

    var hudVisible: Bool { hud?.isVisible ?? false }

    func toggleHUD() {
        if hudVisible { closeHUD() } else if Recorder.shared.isRecording { showHUD() }
    }

    // MARK: live session window

    func showLiveWindow() {
        if live == nil {
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 660, height: 620),
                styleMask: [.titled, .closable, .resizable, .fullSizeContentView],
                backing: .buffered, defer: false)
            w.title = "SESSION.live.md"
            w.titlebarAppearsTransparent = true
            w.titleVisibility = .hidden
            w.appearance = NSAppearance(named: .darkAqua)
            w.isReleasedWhenClosed = false
            // The app never activates while recording, so a normal-level
            // window would sit behind whatever is being demonstrated.
            w.level = .floating
            w.contentView = NSHostingView(
                rootView: LiveWindowView().environmentObject(Recorder.shared)
                    .environmentObject(AppSettings.shared))
            if let screen = NSScreen.main {
                let f = screen.visibleFrame
                w.setFrameOrigin(NSPoint(x: f.minX + 40, y: f.maxY - 660))
            }
            live = w
        }
        live?.makeKeyAndOrderFront(nil)
        refreshActivationPolicy()
    }

    func closeLiveWindow() {
        live?.orderOut(nil)
        live = nil
        refreshActivationPolicy()
    }

    var liveVisible: Bool { live?.isVisible ?? false }

    func toggleLiveWindow() {
        if liveVisible { closeLiveWindow() } else { showLiveWindow() }
    }

    // MARK: library

    func showLibrary(selecting id: String? = nil) {
        if library == nil {
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 1180, height: 760),
                styleMask: [.titled, .closable, .resizable, .miniaturizable,
                            .fullSizeContentView],
                backing: .buffered, defer: false)
            w.title = "Recgo"
            w.titlebarAppearsTransparent = true
            w.titleVisibility = .hidden
            w.appearance = NSAppearance(named: .darkAqua)
            w.isReleasedWhenClosed = false
            w.center()
            w.contentView = NSHostingView(
                rootView: LibraryView().environmentObject(AppSettings.shared))
            library = w
        }
        LibraryStore.shared.reload(selecting: id)
        refreshActivationPolicy()
        library?.makeKeyAndOrderFront(nil)
        library?.orderFrontRegardless()
        NSApp.activate(ignoringOtherApps: true)
    }

    // ⌘Q's "close everything but stay in the menu bar" option.
    func closeAuxiliary() {
        library?.orderOut(nil)
        settings?.orderOut(nil)
        closeLiveWindow()
    }

    // MARK: settings

    func showSettings() {
        if settings == nil {
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 860, height: 600),
                styleMask: [.titled, .closable, .fullSizeContentView],
                backing: .buffered, defer: false)
            w.title = "Settings"
            w.titlebarAppearsTransparent = true
            w.titleVisibility = .hidden
            w.appearance = NSAppearance(named: .darkAqua)
            w.isReleasedWhenClosed = false
            w.center()
            w.contentView = NSHostingView(
                rootView: SettingsView().environmentObject(AppSettings.shared))
            settings = w
        }
        refreshActivationPolicy()
        settings?.makeKeyAndOrderFront(nil)
        settings?.orderFrontRegardless()
        NSApp.activate(ignoringOtherApps: true)
    }
}
