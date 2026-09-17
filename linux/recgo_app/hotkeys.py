import subprocess

from PySide6.QtCore import SLOT, QObject, Qt, Slot
from PySide6.QtDBus import QDBusConnection, QDBusMessage
from PySide6.QtGui import QKeySequence

from recgo_app.settings import AppSettings

COMPONENT = "recgo"
COMPONENT_FRIENDLY = "Recgo"
SERVICE = "org.kde.kglobalaccel"
SET_PRESENT = 2
NO_AUTOLOADING = 4

MODIFIERS = {
    "ctrl": "Ctrl", "control": "Ctrl", "⌃": "Ctrl",
    "shift": "Shift", "⇧": "Shift",
    "alt": "Alt", "opt": "Alt", "option": "Alt", "⌥": "Alt",
    "cmd": "Meta", "command": "Meta", "meta": "Meta", "super": "Meta", "win": "Meta", "⌘": "Meta",
}

KEYS = {**{c: c.upper() for c in "abcdefghijklmnopqrstuvwxyz"},
        **{c: c for c in "0123456789"},
        "return": "Return", "enter": "Return", "⏎": "Return", "space": "Space", "tab": "Tab",
        "escape": "Esc", "esc": "Esc", "delete": "Backspace", "backspace": "Backspace",
        "left": "Left", "right": "Right", "up": "Up", "down": "Down",
        **{"f%d" % i: "F%d" % i for i in range(1, 13)}}


class Chord:
    def __init__(self, seq):
        self.seq = seq

    @property
    def key(self):
        return self.seq[0].toCombined()

    @property
    def label(self):
        return self.seq.toString(QKeySequence.SequenceFormat.NativeText)

    @property
    def portable(self):
        return self.seq.toString(QKeySequence.SequenceFormat.PortableText)


def parse(spec):
    s = spec.strip().lower()
    for sym in ("⌘", "⇧", "⌥", "⌃"):
        s = s.replace(sym, sym + "+")
    tokens = [t for t in s.replace("-", "+").replace(" ", "+").split("+") if t]
    mods = []
    key = None
    for t in tokens:
        if t in MODIFIERS:
            if MODIFIERS[t] not in mods:
                mods.append(MODIFIERS[t])
        elif key is None and t in KEYS:
            key = KEYS[t]
        else:
            return None
    if not mods or key is None:
        return None
    order = ["Meta", "Ctrl", "Alt", "Shift"]
    mods.sort(key=order.index)
    seq = QKeySequence.fromString("+".join(mods + [key]), QKeySequence.SequenceFormat.PortableText)
    if seq.count() != 1:
        return None
    return Chord(seq)


def label_for(settings_key):
    c = parse(AppSettings.shared().get(settings_key))
    return c.label if c else ""


def busctl(*args):
    try:
        r = subprocess.run(["busctl", "--user", "--timeout=2", "call", SERVICE] + list(args),
                           capture_output=True, text=True, timeout=5)
    except (OSError, subprocess.TimeoutExpired):
        return None
    if r.returncode != 0:
        return None
    return r.stdout.strip()


class Hotkeys(QObject):
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        self.actions = []
        self.available = False
        self.bound = {}
        self.conflicts = set()
        self._connected = False

    def define(self, actions):
        self.actions = list(actions)

    def _action_id(self, action):
        return [COMPONENT, action.settings_key, COMPONENT_FRIENDLY, action.name]

    def _connect_signal(self):
        if self._connected:
            return
        bus = QDBusConnection.sessionBus()
        ok = bus.connect(SERVICE, "/component/" + COMPONENT, "org.kde.kglobalaccel.Component",
                         "globalShortcutPressed", self, SLOT("pressed(QDBusMessage)"))
        self._connected = bool(ok)

    @Slot(QDBusMessage)
    def pressed(self, msg):
        args = msg.arguments()
        if len(args) < 2 or args[0] != COMPONENT:
            return
        for a in self.actions:
            if a.settings_key == args[1]:
                a.action()
                return

    def register(self):
        self.bound = {}
        self.conflicts = set()
        s = AppSettings.shared()
        wanted = {a.settings_key: parse(s.get(a.settings_key)) for a in self.actions}
        if not any(wanted.values()):
            for a in self.actions:
                self._release(a)
            return
        for a in self.actions:
            chord = wanted[a.settings_key]
            if chord is None:
                self._release(a)
                continue
            ident = self._action_id(a)
            if busctl("/kglobalaccel", "org.kde.KGlobalAccel", "doRegister", "as", "4", *ident) is None:
                self.available = False
                return
            self.available = True
            got = busctl("/kglobalaccel", "org.kde.KGlobalAccel", "setShortcut", "asaiu", "4", *ident,
                         "1", str(chord.key), str(SET_PRESENT | NO_AUTOLOADING))
            if got is None:
                continue
            parts = got.split()
            keys = [int(x) for x in parts[2:]] if len(parts) >= 2 else []
            if keys and keys[0] == chord.key:
                self.bound[a.settings_key] = chord
            else:
                self.conflicts.add(a.settings_key)
        self._connect_signal()

    def _release(self, action):
        ident = self._action_id(action)
        busctl("/kglobalaccel", "org.kde.KGlobalAccel", "setShortcut", "asaiu", "4", *ident, "0",
               str(SET_PRESENT | NO_AUTOLOADING))
        busctl("/kglobalaccel", "org.kde.KGlobalAccel", "setInactive", "as", "4", *ident)

    def shutdown(self):
        for a in self.actions:
            if a.settings_key in self.bound:
                busctl("/kglobalaccel", "org.kde.KGlobalAccel", "setInactive", "as", "4",
                       *self._action_id(a))


class HotkeyAction:
    def __init__(self, name, settings_key, action):
        self.name = name
        self.settings_key = settings_key
        self.action = action


_ = Qt
