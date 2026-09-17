import Carbon
import AppKit

struct HotkeyAction {
    let name: String
    let settingsKey: String
    let action: () -> Void
}

struct HotkeyChord: Equatable {
    let keyCode: UInt32
    let carbonModifiers: UInt32
    let label: String
}

// Global hotkeys via Carbon RegisterEventHotKey: no Accessibility grant
// needed, works while any app is frontmost. Nothing ships bound — a
// system-wide key grab is opt-in per action, from Settings → Shortcuts or
// `defaults write dev.eordano.recgo shortcutRecordScreen cmd+shift+1`.
final class Hotkeys {
    static let shared = Hotkeys()

    static let actions: [HotkeyAction] = [
        HotkeyAction(name: "Record screen", settingsKey: "shortcutRecordScreen") {
            Actions.start(.screen)
        },
        HotkeyAction(name: "Record one screen", settingsKey: "shortcutRecordWindow") {
            Actions.start(.window)
        },
        HotkeyAction(name: "Record browser", settingsKey: "shortcutRecordBrowser") {
            Actions.start(.browser)
        },
        HotkeyAction(name: "Record this tab", settingsKey: "shortcutRecordTab") {
            Actions.start(.tab)
        },
        HotkeyAction(name: "Record audio only", settingsKey: "shortcutRecordAudio") {
            Actions.start(.audio)
        },
        HotkeyAction(name: "Mark this moment", settingsKey: "shortcutMark") {
            Actions.mark()
        },
        HotkeyAction(name: "Hide or show the HUD", settingsKey: "shortcutToggleHUD") {
            Windows.shared.toggleHUD()
        },
        HotkeyAction(name: "Stop and open session", settingsKey: "shortcutStop") {
            Actions.stop()
        },
        HotkeyAction(name: "Open Library", settingsKey: "shortcutOpenLibrary") {
            Windows.shared.showLibrary()
        },
    ]

    static func chord(forKey settingsKey: String) -> HotkeyChord? {
        parse(UserDefaults.standard.string(forKey: settingsKey) ?? "")
    }

    static func label(forKey settingsKey: String) -> String {
        chord(forKey: settingsKey)?.label ?? ""
    }

    // "cmd+shift+1" (or "⌘⇧1") → keycode + modifiers + display label. A
    // chord needs at least one modifier: a bare letter grabbed system-wide
    // would eat normal typing everywhere.
    static func parse(_ spec: String) -> HotkeyChord? {
        var s = spec.lowercased()
        for sym in ["⌘", "⇧", "⌥", "⌃"] {
            s = s.replacingOccurrences(of: sym, with: sym + "+")
        }
        let tokens = s.split(whereSeparator: { "+- ".contains($0) }).map(String.init)
        var modifiers: UInt32 = 0
        var key: String?
        for t in tokens {
            switch t {
            case "cmd", "command", "⌘": modifiers |= UInt32(cmdKey)
            case "shift", "⇧": modifiers |= UInt32(shiftKey)
            case "alt", "opt", "option", "⌥": modifiers |= UInt32(optionKey)
            case "ctrl", "control", "⌃": modifiers |= UInt32(controlKey)
            default:
                guard key == nil else { return nil }
                key = t
            }
        }
        guard modifiers != 0, let key, let (code, glyph) = keyTable[key] else { return nil }
        var label = ""
        if modifiers & UInt32(controlKey) != 0 { label += "⌃" }
        if modifiers & UInt32(optionKey) != 0 { label += "⌥" }
        if modifiers & UInt32(shiftKey) != 0 { label += "⇧" }
        if modifiers & UInt32(cmdKey) != 0 { label += "⌘" }
        return HotkeyChord(keyCode: code, carbonModifiers: modifiers, label: label + glyph)
    }

    private static let keyTable: [String: (UInt32, String)] = {
        var t: [String: (UInt32, String)] = [
            "a": (UInt32(kVK_ANSI_A), "A"), "b": (UInt32(kVK_ANSI_B), "B"),
            "c": (UInt32(kVK_ANSI_C), "C"), "d": (UInt32(kVK_ANSI_D), "D"),
            "e": (UInt32(kVK_ANSI_E), "E"), "f": (UInt32(kVK_ANSI_F), "F"),
            "g": (UInt32(kVK_ANSI_G), "G"), "h": (UInt32(kVK_ANSI_H), "H"),
            "i": (UInt32(kVK_ANSI_I), "I"), "j": (UInt32(kVK_ANSI_J), "J"),
            "k": (UInt32(kVK_ANSI_K), "K"), "l": (UInt32(kVK_ANSI_L), "L"),
            "m": (UInt32(kVK_ANSI_M), "M"), "n": (UInt32(kVK_ANSI_N), "N"),
            "o": (UInt32(kVK_ANSI_O), "O"), "p": (UInt32(kVK_ANSI_P), "P"),
            "q": (UInt32(kVK_ANSI_Q), "Q"), "r": (UInt32(kVK_ANSI_R), "R"),
            "s": (UInt32(kVK_ANSI_S), "S"), "t": (UInt32(kVK_ANSI_T), "T"),
            "u": (UInt32(kVK_ANSI_U), "U"), "v": (UInt32(kVK_ANSI_V), "V"),
            "w": (UInt32(kVK_ANSI_W), "W"), "x": (UInt32(kVK_ANSI_X), "X"),
            "y": (UInt32(kVK_ANSI_Y), "Y"), "z": (UInt32(kVK_ANSI_Z), "Z"),
            "0": (UInt32(kVK_ANSI_0), "0"), "1": (UInt32(kVK_ANSI_1), "1"),
            "2": (UInt32(kVK_ANSI_2), "2"), "3": (UInt32(kVK_ANSI_3), "3"),
            "4": (UInt32(kVK_ANSI_4), "4"), "5": (UInt32(kVK_ANSI_5), "5"),
            "6": (UInt32(kVK_ANSI_6), "6"), "7": (UInt32(kVK_ANSI_7), "7"),
            "8": (UInt32(kVK_ANSI_8), "8"), "9": (UInt32(kVK_ANSI_9), "9"),
            "return": (UInt32(kVK_Return), "⏎"), "enter": (UInt32(kVK_Return), "⏎"),
            "⏎": (UInt32(kVK_Return), "⏎"),
            "space": (UInt32(kVK_Space), "␣"),
            "tab": (UInt32(kVK_Tab), "⇥"),
            "escape": (UInt32(kVK_Escape), "⎋"), "esc": (UInt32(kVK_Escape), "⎋"),
            "delete": (UInt32(kVK_Delete), "⌫"),
            "left": (UInt32(kVK_LeftArrow), "←"), "right": (UInt32(kVK_RightArrow), "→"),
            "up": (UInt32(kVK_UpArrow), "↑"), "down": (UInt32(kVK_DownArrow), "↓"),
        ]
        let fkeys = [kVK_F1, kVK_F2, kVK_F3, kVK_F4, kVK_F5, kVK_F6,
                     kVK_F7, kVK_F8, kVK_F9, kVK_F10, kVK_F11, kVK_F12]
        for (i, code) in fkeys.enumerated() {
            t["f\(i + 1)"] = (UInt32(code), "F\(i + 1)")
        }
        return t
    }()

    private var refs: [EventHotKeyRef] = []
    private var handlerInstalled = false

    // Idempotent: drops every current grab, then registers only the chords
    // configured right now. Call again whenever a shortcut setting changes.
    func register() {
        for ref in refs { UnregisterEventHotKey(ref) }
        refs = []
        let bound = Hotkeys.actions.enumerated().compactMap { i, a in
            Hotkeys.chord(forKey: a.settingsKey).map { (i, $0) }
        }
        if bound.isEmpty { return }
        installHandler()
        for (i, chord) in bound {
            var ref: EventHotKeyRef?
            let id = EventHotKeyID(signature: OSType(0x5243_474F), id: UInt32(i))
            RegisterEventHotKey(
                chord.keyCode, chord.carbonModifiers, id, GetEventDispatcherTarget(), 0, &ref)
            if let ref { refs.append(ref) }
        }
    }

    private func installHandler() {
        guard !handlerInstalled else { return }
        handlerInstalled = true
        var eventType = EventTypeSpec(
            eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(
            GetEventDispatcherTarget(),
            { _, event, _ -> OSStatus in
                var hkID = EventHotKeyID()
                GetEventParameter(
                    event, EventParamName(kEventParamDirectObject),
                    EventParamType(typeEventHotKeyID), nil,
                    MemoryLayout<EventHotKeyID>.size, nil, &hkID)
                let idx = Int(hkID.id)
                if idx < Hotkeys.actions.count {
                    DispatchQueue.main.async { Hotkeys.actions[idx].action() }
                }
                return noErr
            },
            1, &eventType, nil, nil)
    }
}
