import json
import os
import subprocess

from PySide6.QtCore import QProcess, Qt, Signal
from PySide6.QtGui import QColor, QFont, QKeySequence, QShortcut
from PySide6.QtWidgets import QComboBox, QFileDialog, QLineEdit, QScrollArea, QStackedWidget, QWidget

from recgo_app import hotkeys
from recgo_app.hotkeys import Hotkeys
from recgo_app.settings import (RECORDER_CONF, SHORTCUT_KEYS, AppSettings, RecordMode, recorder_config,
                                whisper_models)
from recgo_app.theme import Theme, css, white
from recgo_app.widgets import (Button, HoverRow, Segment, ShortcutCapture, Toggle, hbox, highlight, label,
                               mono_field, open_path, set_color, vbox, vline)

PANES = [
    ("General", "Start-up, the tray menu, session titles and where the recorders are."),
    ("Recording", "The default mode and which moments get a screenshot."),
    ("Audio", "The microphone and what else is mixed into the narration track."),
    ("Transcription", "How narration becomes text: the backend, model and language."),
    ("Data & sharing", "Everything stays on this computer unless something on this page is on."),
    ("Shortcuts", "System-wide keys, registered through KGlobalAccel."),
    ("Library", "Where sessions are kept and how many there are."),
]

SHORTCUT_ROWS = [("Record screen", "shortcutRecordScreen"), ("Record one screen", "shortcutRecordWindow"),
                 ("Record browser", "shortcutRecordBrowser"), ("Record this tab", "shortcutRecordTab"),
                 ("Record audio only", "shortcutRecordAudio"),
                 ("Mark this moment", "shortcutMark"), ("Hide or show the HUD", "shortcutToggleHUD"),
                 ("Stop and open session", "shortcutStop"), ("Open Library", "shortcutOpenLibrary")]


LANGUAGES = [("", "Autodetect"), ("en", "English"), ("es", "Spanish"), ("de", "German"), ("fr", "French"),
             ("it", "Italian"), ("pt", "Portuguese"), ("nl", "Dutch"), ("ca", "Catalan"), ("pl", "Polish"),
             ("ru", "Russian"), ("uk", "Ukrainian"), ("tr", "Turkish"), ("ja", "Japanese"), ("zh", "Chinese"),
             ("ko", "Korean"), ("hi", "Hindi"), ("ar", "Arabic")]


def whisper_binaries():
    import shutil
    out = []
    for name in ("whisper-cli", "whisper-cpp", "whisper"):
        p = shutil.which(name)
        if p and p not in out:
            out.append(p)
    return out


def pulse_sources():
    try:
        r = subprocess.run(["pactl", "-f", "json", "list", "sources"], capture_output=True, text=True,
                           timeout=4)
        data = json.loads(r.stdout) if r.returncode == 0 else []
    except (OSError, ValueError, subprocess.TimeoutExpired):
        return []
    return [(s.get("name", ""), s.get("description", "")) for s in data if s.get("name")]


class SettingsWindow(QWidget):
    closed = Signal()

    def __init__(self):
        super().__init__(None, Qt.WindowType.Window)
        self.setWindowTitle("Recgo Settings")
        self.setFixedSize(860, 600)
        self.setStyleSheet("background: %s;" % css(Theme.bg))
        self.s = AppSettings.shared()
        self._rows = {}
        self._chips = {}
        self._entries = []
        self._pane = 0
        root = hbox(self)
        side = QWidget()
        side.setFixedWidth(204)
        sl = vbox(side, (10, 14, 10, 10), 2)
        sl.setAlignment(Qt.AlignmentFlag.AlignTop)
        self.search = mono_field(QLineEdit())
        self.search.setPlaceholderText("Search settings")
        self.search.setClearButtonEnabled(True)
        self.search.setFixedHeight(32)
        self.search.setToolTip("Show only the settings whose name or explanation contains this (Ctrl+F)")
        self.search.textChanged.connect(self._apply_search)
        sl.addWidget(self.search)
        sl.addSpacing(10)
        QShortcut(QKeySequence.StandardKey.Find, self, activated=self._focus_search)
        self.side_rows = []
        for i, (name, _) in enumerate(PANES):
            row = HoverRow(radius=9, hover=white(0.07))
            row.setMinimumHeight(40)
            rl = hbox(row, (11, 8, 11, 8))
            lb = label(name, 13, QFont.Weight.DemiBold, Theme.secondary)
            rl.addWidget(lb)
            row.clicked.connect(lambda i=i: self.select(i))
            sl.addWidget(row)
            self.side_rows.append((row, lb))
        root.addWidget(side)
        root.addWidget(vline())
        self.stack = QStackedWidget()
        for i, builder in enumerate((self.general, self.recording, self.audio, self.transcription, self.data,
                                     self.shortcuts, self.library)):
            self._pane = i
            self._entries.append([])
            scroll = QScrollArea()
            scroll.setWidgetResizable(True)
            host = QWidget()
            hl = vbox(host, (28, 14, 28, 32))
            hl.setAlignment(Qt.AlignmentFlag.AlignTop)
            name, blurb = PANES[i]
            head = label(name, 20, QFont.Weight.Bold)
            head.setFixedHeight(32)
            hl.addWidget(head)
            hl.addWidget(label(blurb, 12, color=Theme.faint, wrap=True))
            hl.addSpacing(10)
            builder(hl)
            scroll.setWidget(host)
            self.stack.addWidget(scroll)
        root.addWidget(self.stack, 1)
        self.select(0)
        self.s.changed.connect(self._on_setting)
        geo = self.s.geometry("settings")
        if geo:
            self.restoreGeometry(geo)

    def closeEvent(self, e):
        self.s.save_geometry("settings", self.saveGeometry())
        super().closeEvent(e)
        self.closed.emit()

    def keyPressEvent(self, e):
        if e.key() == Qt.Key.Key_Escape or (e.key() == Qt.Key.Key_W and e.modifiers() & Qt.KeyboardModifier.ControlModifier):
            self.close()
        else:
            super().keyPressEvent(e)

    def select(self, i):
        self.stack.setCurrentIndex(i)
        for j, (row, lb) in enumerate(self.side_rows):
            row.set_base(QColor(255, 45, 85, 41) if i == j else QColor(0, 0, 0, 0))
            set_color(lb, Theme.text if i == j else Theme.secondary)

    def _focus_search(self):
        self.search.setFocus()
        self.search.selectAll()

    def _register(self, w, title, caption):
        """a row the search can find: its title, its explanation, and the
        divider drawn under it"""
        w.search_text = ("%s %s" % (title, caption)).lower()
        w.title_text = title
        w.caption_text = caption
        self._entries[self._pane].append(w)

    def _apply_search(self, text):
        q = text.strip().lower()
        first = None
        for i, rows in enumerate(self._entries):
            hits = 0
            for w in rows:
                hit = not q or q in w.search_text
                w.setVisible(hit)
                if getattr(w, "divider", None) is not None:
                    w.divider.setVisible(hit)
                mark = hit and q
                if getattr(w, "title", None) is not None:
                    w.title.setText(highlight(w.title_text, q) if mark else w.title_text)
                if getattr(w, "caption", None) is not None:
                    w.caption.setText(highlight(w.caption_text, q) if mark else w.caption_text)
                hits += hit
            self.side_rows[i][0].setVisible(not q or hits > 0)
            if hits and first is None:
                first = i
        if q and first is not None and not self.side_rows[self.stack.currentIndex()][0].isVisible():
            self.select(first)

    def _on_setting(self, key):
        self._refresh_privacy()
        for k, w in self._rows.items():
            if k == key and isinstance(w, Toggle) and w.isChecked() != self.s.get(k):
                w.setChecked(self.s.get(k))

    def row(self, lay, title, caption, control, mono=False, last=False):
        w = QWidget()
        rl = hbox(w, (0, 14, 0, 14), 16)
        col = vbox(spacing=3)
        ttl = label(title, 14, QFont.Weight.DemiBold)
        col.addWidget(ttl)
        warn = "PUSHES" in caption or "UPLOADS" in caption or "EXPOSES" in caption
        cap = label(caption, 12, color=Theme.pink if warn else Theme.faint, mono=mono, wrap=True)
        col.addWidget(cap)
        rl.addLayout(col, 1)
        rl.addWidget(control, 0, Qt.AlignmentFlag.AlignVCenter)
        lay.addWidget(w)
        w.divider = None
        if not last:
            line = QWidget()
            line.setFixedHeight(1)
            line.setStyleSheet("background: rgba(255,255,255,0.07);")
            lay.addWidget(line)
            w.divider = line
        w.caption = cap
        w.title = ttl
        self._register(w, title, caption)
        return w

    def toggle_row(self, lay, title, caption, key, last=False, mono=False, on_change=None):
        t = Toggle()
        t.setChecked(bool(self.s.get(key)))

        def changed(on):
            self.s.set(key, bool(on))
            if on_change:
                on_change(on)

        t.toggled.connect(changed)
        self._rows[key] = t
        return self.row(lay, title, caption, t, last=last, mono=mono)

    def field(self, key, placeholder, width):
        f = mono_field(QLineEdit(self.s.get(key)))
        f.setPlaceholderText(placeholder)
        f.setFixedWidth(width)
        f.editingFinished.connect(lambda: self.s.set(key, f.text().strip()))
        self._rows[key] = f
        return f

    def selector(self, key, options, width=260, browse=None, mono=False):
        """A real dropdown for a settings key. options: [(value, label)]; a
        value not in the list is shown as its own entry; browse (title,
        picker) appends a 'Browse…' row that opens a file dialog."""
        combo = QComboBox()
        combo.setFixedWidth(width)
        if mono:
            mono_field(combo)
        current = self.s.get(key)
        values = [v for v, _ in options]
        for v, text in options:
            combo.addItem(text, v)
        if current and current not in values:
            combo.addItem(current, current)
        if browse:
            combo.addItem("Browse…", "\x00browse")
        combo.setCurrentIndex(combo.findData(current if current in values or current else ""))
        combo.setEnabled(not self.s.managed(key))

        def pick(i):
            data = combo.itemData(i)
            if data == "\x00browse":
                title, picker = browse
                chosen = picker(title)
                combo.blockSignals(True)
                if chosen:
                    if combo.findData(chosen) < 0:
                        combo.insertItem(combo.count() - 1, chosen, chosen)
                    combo.setCurrentIndex(combo.findData(chosen))
                    self.s.set(key, chosen)
                else:
                    combo.setCurrentIndex(max(0, combo.findData(self.s.get(key))))
                combo.blockSignals(False)
                return
            self.s.set(key, data or "")

        combo.currentIndexChanged.connect(pick)
        self._rows[key] = combo
        return combo

    def _pick_file(self, title, start=None):
        f, _ = QFileDialog.getOpenFileName(self, title, start or os.path.expanduser("~"))
        return f

    def button(self, text, cb):
        b = Button(text, size=13, height=32, radius=9)
        b.clicked.connect(cb)
        return b

    def banner(self, lay, title, body, tint):
        w = QWidget()
        w.setObjectName("banner")
        w.setStyleSheet("#banner { background: %s; border: 1px solid %s; border-radius: 14px; }"
                        % (css(QColor(tint.red(), tint.green(), tint.blue(), 26)),
                           css(QColor(tint.red(), tint.green(), tint.blue(), 71))))
        wl = vbox(w, (16, 16, 16, 16), 4)
        wl.addWidget(label(title, 14, QFont.Weight.DemiBold, tint))
        wl.addWidget(label(body, 12, color=Theme.secondary, wrap=True))
        lay.addSpacing(18)
        lay.addWidget(w)

    def general(self, lay):
        managed = self.s.autostart_managed
        t = Toggle()
        t.setChecked(self.s.launch_at_login)
        t.setEnabled(not managed)
        t.toggled.connect(lambda on: setattr(self.s, "launch_at_login", bool(on)))
        self.row(lay, "Launch at login", "Managed by the NixOS module (desktop.recgo.app.autostart)." if managed
                 else "Starts the tray icon at login; no window is opened.", t)
        self.toggle_row(lay, "Ask to record Google Meet calls",
                        "Prompts after joining a call in Chromium on debug port 9222. Dismissing silences that call.",
                        "meetPrompt")
        self.toggle_row(lay, "Show elapsed time in the tray menu",
                        "Also shown in the tray tooltip; updated every second.",
                        "menuTimer")
        self.toggle_row(lay, "Title sessions automatically",
                        "The session folder is named from the narration when the session ends. With a remote "
                        "transcription backend the transcript goes to the same endpoint for this; Data & "
                        "sharing says so when it applies.", "autoTitle")
        self.row(lay, "Recorders folder", "Where the recgo command-line recorders live. Blank searches the "
                 "package's helpers and PATH.", self.field("binDir", "auto", 260), last=True)

    def _choose_root(self):
        d = QFileDialog.getExistingDirectory(self, "Session folder", self.s.out_root)
        if d:
            self.s.outRoot = d
            self.folder_row.caption.setText(d)

    def recording(self, lay):
        seg = Segment([(m.value, "Audio" if m == RecordMode.audio else m.label) for m in RecordMode],
                      self.s.default_mode.value)
        seg.changed.connect(lambda k: self.s.set("defaultMode", k))
        self.row(lay, "Default mode", "Used by recgo-app --record with no mode given. Screen records every "
                 "screen; Window records one screen or one window, chosen in the desktop's picker when the "
                 "recording starts.", seg)
        self.toggle_row(lay, "Screenshot on every click",
                        "Reads the mouse from /dev/input, so your user must be in the input group; the pointer "
                        "position and the clicked window come from KWin. Recording still works without it.",
                        "clickShots")
        self.toggle_row(lay, "Also screenshot focus changes and new windows",
                        "A KWin script reports app switches and new windows or dialogs; they become anchors in "
                        "the document.", "focusShots")
        self.toggle_row(lay, "Open the live session window when recording starts",
                        "Otherwise only the HUD is shown; the live document can be opened from the HUD or the "
                        "tray menu.",
                        "liveOnStart", last=True)

    def audio(self, lay):
        self.toggle_row(lay, "Capture the microphone", "Off leaves the session without a narration track.",
                        "captureMic")
        sources = [("", "System default source")]
        for name, desc in pulse_sources():
            sources.append((name, desc or name))
        mics = [(n, d) for n, d in sources if not n.endswith(".monitor")]
        monitors = [("", "Monitor of the default output")] + [(n, d) for n, d in sources if n.endswith(".monitor")]
        self.row(lay, "Microphone device", "The default follows what PipeWire/PulseAudio marks as the default "
                 "source; the list comes from pactl.", self.selector("micDevice", mics, width=320))
        self.toggle_row(lay, "Include system audio", "Mixed into the narration track at unity gain. Can be muted while "
                        "recording from the tray menu, the HUD or recgo-app --system-audio; each toggle adds a "
                        "Note line to the session.", "systemAudio")
        self.row(lay, "System audio source", "A sink's .monitor source. The default follows the default output "
                 "at the moment recording starts.", self.selector("monitorDevice", monitors, width=320),
                 last=True)
        self.banner(lay, "Bluetooth outputs go through a temporary sink",
                    "On PipeWire a Bluetooth monitor yields nothing, so the recorder loads a null sink, makes it "
                    "the default output for the session, loops it back to the headset, and removes it at stop.",
                    Theme.amber)

    def transcription(self, lay):
        backends = [("auto", "Auto (local if a model is present)"),
                    ("local", "Local (whisper.cpp here)"),
                    ("remote", "Remote (batch upload)"),
                    ("realtime", "Realtime (as you speak)"),
                    ("none", "None")]
        self.row(lay, "Backend", "Local runs whisper.cpp on this machine, a few seconds behind speech. Remote and "
                 "realtime UPLOAD THE AUDIO to the configured endpoint; realtime returns words as they are "
                 "spoken and is what the portal uses." + (" Set by the NixOS config." if self.s.managed("sttBackend") else ""),
                 self.selector("sttBackend", backends, width=300))
        models = [("", "Discovered (newest ggml in the models dir)")]
        for m in whisper_models():
            models.append((m, os.path.basename(m)))
        self.row(lay, "Whisper model", "The ggml file whisper.cpp loads for local transcription.",
                 self.selector("whisperModel", models, width=300,
                               browse=("Whisper model", lambda t: self._pick_file(t, os.path.expanduser("~/.local/share/recgo/models")))))
        bins = [("", "whisper-cli on PATH")]
        for b in whisper_binaries():
            bins.append((b, b))
        self.row(lay, "whisper.cpp binary", "Which whisper.cpp build runs local transcription.",
                 self.selector("whisperBin", bins, width=300, browse=("whisper.cpp binary", self._pick_file)))
        self.row(lay, "Language", "Autodetect costs about a second at the start of each utterance.",
                 self.selector("sttLanguage", LANGUAGES, width=220))
        self.toggle_row(lay, "Show narration live in the HUD",
                        "Text appears as each utterance is decoded.", "liveNarrationHUD", last=True)

    def data(self, lay):
        self.summary_card = QWidget()
        self.summary_card.setObjectName("summaryCard")
        sl = vbox(self.summary_card, (16, 14, 16, 14), 6)
        self.summary_title = label("", 14, QFont.Weight.DemiBold)
        sl.addWidget(self.summary_title)
        self.summary_lines = label("", 12, color=Theme.secondary, wrap=True)
        sl.addWidget(self.summary_lines)
        lay.addWidget(self.summary_card)
        self.summary_card.title = self.summary_title
        self.summary_card.divider = None
        self._register(self.summary_card, "Where this computer's bytes go",
                       "the summary the tray menu, the HUD and the live window show")
        lay.addSpacing(14)
        t = Toggle()
        t.setChecked(self.s.neverUpload)
        t.setEnabled(not self.s.managed("neverUpload"))
        if self.s.managed("neverUpload"):
            t.setToolTip("Set by the NixOS config (desktop.recgo.app.managed)")
        t.toggled.connect(lambda on: self.s.set("neverUpload", bool(on)))
        self._rows["neverUpload"] = t
        self.never_row = self.row(lay, "Never upload anything",
                                  "Forces local transcription and keeps the portal and sync off, whatever "
                                  "the rows below say.", t)
        self.never_title = self.never_row.title
        self.privacy_group = QWidget()
        gl = vbox(self.privacy_group)
        config = recorder_config()
        endpoint = ((config.get("transcription") or {}).get("remote") or {}).get("endpoint", "")
        self.endpoint_row = self.row(gl, "Remote transcription endpoint",
                                     endpoint or "No endpoint in the recorders' config.toml: their built-in list is used.",
                                     self.button("Open config", lambda: open_path(os.path.dirname(RECORDER_CONF))),
                                     mono=bool(endpoint))
        self.endpoint_row.setToolTip("Remote and realtime backends UPLOAD THE AUDIO here. Read from %s; the "
                                     "NixOS module writes it on fleet hosts." % RECORDER_CONF)
        self.sync_row = self.toggle_row(gl, "Sync finished sessions to a host",
                                        self.s.syncTarget or "PUSHES THE WHOLE SESSION over ssh when set.",
                                        "syncEnabled", mono=bool(self.s.syncTarget))
        self.row(gl, "Sync target", "user@host:/path, handed to rsync over ssh.",
                 self.field("syncTarget", "host:/srv/sessions", 260))
        self.row(gl, "Sync ssh key", "A dedicated identity that needs no touch, tried before the config ones; "
                 "a YubiKey key here blocks every session end.", self.field("syncKey", "~/.ssh/id_sync", 260))
        url = self.field("syncURL", "https://host/walk-and-talk", 260)
        url.setEnabled(not self.s.managed("syncURL"))
        self.row(gl, "Sessions URL", "Where the synced session folders are served. Adds Copy image URL and "
                 "Copy session URL to the Library" + (". Set by the NixOS config." if self.s.managed("syncURL") else "."),
                 url)
        self.toggle_row(gl, "Open a portal room while recording",
                        "An agent in the room reads the session document as it is written. The room name is "
                        "the only credential, and it EXPOSES THE OUTPUT ROOT: every session, read-only, to "
                        "everyone in the room for as long as the recording runs.", "portalEnabled")
        self.row(gl, "Portal URL", "The wss endpoint of the portal service.",
                 self.field("portalURL", "wss://portal.example/ws", 280))
        self.row(gl, "Room", "Blank mints an unguessable room name per session.",
                 self.field("portalRoom", "walk-lantern-42", 220), last=True)
        lay.addWidget(self.privacy_group)
        self._refresh_privacy()

    def _refresh_privacy(self):
        if not hasattr(self, "summary_card"):
            return
        lines = self.s.data_lines()
        leaves = any(l for _, l in lines)
        tint = Theme.amber if leaves else Theme.green
        self.summary_title.setText("Some bytes leave this computer" if leaves else "Nothing leaves this computer")
        set_color(self.summary_title, tint)
        self.summary_lines.setText("\n".join(t for t, _ in lines))
        self.summary_card.setStyleSheet("#summaryCard { background: %s; border: 1px solid %s; border-radius: 14px; }"
                                        % (css(QColor(tint.red(), tint.green(), tint.blue(), 26)),
                                           css(QColor(tint.red(), tint.green(), tint.blue(), 69))))
        self.privacy_group.setEnabled(not self.s.neverUpload)
        self.sync_row.caption.setText(self.s.syncTarget or "PUSHES THE WHOLE SESSION over ssh when set.")

    def shortcuts(self, lay):
        for i, (name, key) in enumerate(SHORTCUT_ROWS):
            w = QWidget()
            rl = hbox(w, (0, 11, 0, 11), 12)
            w.title = label(name, 14, QFont.Weight.DemiBold)
            rl.addWidget(w.title)
            rl.addStretch()
            chip = label("", 12, QFont.Weight.DemiBold, Theme.amber)
            rl.addWidget(chip)
            self._chips[key] = chip
            cap = ShortcutCapture(self.s.get(key))
            if self.s.shortcut_managed(key):
                cap.setEnabled(False)
                cap.setToolTip("Set by desktop.recgo.shortcuts in the NixOS config")
            cap.changed.connect(lambda spec, key=key: self._set_shortcut(key, spec))
            rl.addWidget(cap)
            lay.addWidget(w)
            w.divider = None
            if i < len(SHORTCUT_ROWS) - 1:
                line = QWidget()
                line.setFixedHeight(1)
                line.setStyleSheet("background: rgba(255,255,255,0.07);")
                lay.addWidget(line)
                w.divider = line
            self._register(w, name, "shortcut " + self.s.get(key))
        self._refresh_chips()

    def _set_shortcut(self, key, spec):
        if self.s.get(key) != spec:
            self.s.set(key, spec)
            Hotkeys.shared().register()
        self._refresh_chips()

    def _refresh_chips(self):
        hk = Hotkeys.shared()
        for key, chip in self._chips.items():
            spec = self.s.get(key)
            chord = hotkeys.parse(spec)
            if not spec:
                chip.hide()
            elif chord is None:
                chip.setText("not a chord")
                chip.show()
            elif key in hk.conflicts:
                chip.setText("taken by another app")
                chip.show()
            elif not hk.available:
                chip.setText("KGlobalAccel unavailable")
                chip.show()
            else:
                chip.hide()

    def library(self, lay):
        choose = self.button("Choose…", self._choose_root)
        choose.setEnabled(not self.s.managed("outRoot"))
        if self.s.managed("outRoot"):
            choose.setToolTip("Set by the NixOS config (desktop.recgo.outputDir); the CLIs read the same folder")
        self.folder_row = self.row(lay, "Session folder", self.s.out_root, choose, mono=True)
        from recgo_app.library import LibraryStore
        store = LibraryStore.shared()
        if not store.sessions:
            store.reload()
        self.row(lay, "Storage used", "%d sessions in %s" % (len(store.sessions), os.path.basename(self.s.out_root)),
                 self.button("Open folder", lambda: open_path(self.s.out_root)))
        self.row(lay, "HTML index", "recgo-sessions writes a self-contained index.html viewer into the session "
                 "folder, for reading the library without this app.",
                 self.button("Regenerate", self._regenerate), last=True)

    def _regenerate(self):
        binary = self.s.resolve_binary("recgo-sessions")
        if binary:
            QProcess.startDetached(binary, ["-dir", self.s.out_root])


_ = SHORTCUT_KEYS
