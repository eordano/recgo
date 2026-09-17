import os

from PySide6.QtCore import QObject, QTimer, Qt
from PySide6.QtGui import QAction, QGuiApplication, QIcon, QKeySequence

from recgo_app.widgets import open_path, reveal
from PySide6.QtWidgets import (QDialog, QDialogButtonBox, QListWidget, QListWidgetItem, QMenu, QMessageBox,
                               QSystemTrayIcon, QVBoxLayout)

from recgo_app import hotkeys
from recgo_app.cdp import CDP
from recgo_app.model import clock_string
from recgo_app.recorder import Phase, Recorder
from recgo_app.settings import AppSettings, RecordMode, data_summary
from recgo_app.widgets import icon, tray_pixmap


class TabPicker(QDialog):
    """Which open tab to pin the recording to: one row per page target,
    title over URL, Enter or a double click picks."""

    def __init__(self, tabs, parent=None):
        super().__init__(parent)
        self.setWindowTitle("Record which tab?")
        self.setMinimumWidth(560)
        self.chosen = None
        lay = QVBoxLayout(self)
        self.list = QListWidget()
        self.list.setAlternatingRowColors(True)
        for t in tabs:
            item = QListWidgetItem("%s\n%s" % (t["title"], t["url"]) if t["title"] else t["url"])
            item.setData(Qt.ItemDataRole.UserRole, t)
            item.setToolTip(t["url"])
            self.list.addItem(item)
        self.list.setCurrentRow(0)
        self.list.itemDoubleClicked.connect(lambda _: self.accept())
        lay.addWidget(self.list)
        buttons = QDialogButtonBox(QDialogButtonBox.StandardButton.Ok | QDialogButtonBox.StandardButton.Cancel)
        buttons.button(QDialogButtonBox.StandardButton.Ok).setText("Record this tab")
        buttons.accepted.connect(self.accept)
        buttons.rejected.connect(self.reject)
        lay.addWidget(buttons)

    def accept(self):
        item = self.list.currentItem()
        self.chosen = item.data(Qt.ItemDataRole.UserRole) if item else None
        super().accept()


class Actions:
    @staticmethod
    def ask_name():
        """Audio only is `recgo <name>`: the name is the one thing it asks
        for, and it is the file's identity wherever the recording lands."""
        from PySide6.QtWidgets import QInputDialog
        s = AppSettings.shared()
        text, ok = QInputDialog.getText(None, "Recgo — Audio only",
                                        "Recording name (the file is <date>-<name>.mkv, like `recgo <name>`):",
                                        text=s.audioName or "audio")
        if not ok:
            return None
        s.set("audioName", text.strip())
        return text

    @staticmethod
    def start(mode, target=None, name=""):
        rec = Recorder.shared()
        if rec.is_busy:
            return
        if mode.needs_cdp:
            def after(up):
                if rec.is_busy:
                    return
                if not up:
                    Actions.explain_cdp(mode)
                elif mode == RecordMode.tab and target is None:
                    Actions.pick_tab()
                else:
                    rec.start(mode, target)
            CDP.shared().refresh(after)
            return
        if mode == RecordMode.audio and not name:
            name = Actions.ask_name()
            if name is None:
                return
        rec.start(mode, name=name)

    @staticmethod
    def pick_tab():
        rec = Recorder.shared()
        tabs = CDP.shared().tabs
        if not tabs:
            box = QMessageBox()
            box.setWindowTitle("Recgo")
            box.setText("No recordable tab")
            box.setInformativeText("The browser on 127.0.0.1:9222 has no page open that recgo-tab can attach to "
                                   "(chrome:// and extension pages do not count). Open the page first.")
            box.exec()
            return
        if len(tabs) == 1:
            rec.start(RecordMode.tab, tabs[0])
            return
        dlg = TabPicker(tabs)
        dlg.setWindowIcon(QIcon(tray_pixmap("idle")))
        if dlg.exec() == QDialog.DialogCode.Accepted and dlg.chosen and not rec.is_busy:
            rec.start(RecordMode.tab, dlg.chosen)

    @staticmethod
    def explain_cdp(mode=None):
        import shutil
        from PySide6.QtCore import QProcess
        box = QMessageBox()
        box.setWindowTitle("Recgo")
        box.setText("%s needs a browser on the DevTools port" % (mode.label if mode else "Browser and Tab"))
        box.setInformativeText(
            "Browser and Tab modes attach to a Chromium-family browser (Chromium, Chrome, Brave, Edge) over "
            "the DevTools protocol on 127.0.0.1:9222, and nothing answered there.\n\n"
            "The flag only takes effect at launch, so quit the browser that is already running and start it with\n\n"
            "    chromium --remote-debugging-port=9222\n\n"
            "Recgo checks the port every two seconds; the rows enable themselves as soon as it answers.")
        launch = None
        chromium = shutil.which("chromium") or shutil.which("google-chrome") or shutil.which("brave")
        if chromium:
            launch = box.addButton("Launch %s with debugging" % os.path.basename(chromium),
                                   QMessageBox.ButtonRole.AcceptRole)
        box.addButton("OK", QMessageBox.ButtonRole.RejectRole)
        box.exec()
        if launch is not None and box.clickedButton() is launch:
            QProcess.startDetached(chromium, ["--remote-debugging-port=9222"])

    @staticmethod
    def stop():
        Recorder.shared().stop()

    @staticmethod
    def mark():
        Recorder.shared().mark()

    @staticmethod
    def system_audio(spec="toggle"):
        """Idle: flips the setting for the next session. Recording: mutes or
        unmutes the monitor mix in place, when one was started."""
        rec = Recorder.shared()
        s = AppSettings.shared()
        if rec.phase == Phase.recording:
            want = {"on": True, "off": False}.get(spec, not rec.system_audio)
            if want == rec.system_audio or not rec.system_audio_available:
                return rec.system_audio_available and rec.system_audio
            return rec.toggle_system_audio()
        if rec.phase == Phase.idle:
            want = {"on": True, "off": False}.get(spec, not s.systemAudio)
            s.set("systemAudio", want)
            return want
        return None


class TrayController(QObject):
    def __init__(self, windows, quit_cb, parent=None):
        super().__init__(parent)
        self.windows = windows
        self.quit_cb = quit_cb
        self._icons = {s: QIcon(tray_pixmap(s)) for s in ("idle", "recording", "finishing")}
        self.tray = QSystemTrayIcon(self._icons["idle"], self)
        self.menu = QMenu()
        self.tray.setContextMenu(self.menu)
        self.tray.activated.connect(self._activated)
        self.tray.messageClicked.connect(lambda: self.windows.show_library())
        self.menu.aboutToShow.connect(self._rebuild)
        rec = Recorder.shared()
        rec.phase_changed.connect(lambda _: self.refresh())
        rec.tick.connect(self.refresh)
        CDP.shared().changed.connect(lambda _: self._rebuild())
        self._cdp_timer = QTimer(self)
        self._cdp_timer.setInterval(2000)
        self._cdp_timer.timeout.connect(lambda: CDP.shared().refresh())
        self._cdp_timer.start()
        CDP.shared().refresh()
        self._state = None
        self._rebuild()
        self.refresh()
        self.tray.show()

    def refresh(self):
        rec = Recorder.shared()
        state = rec.phase.value
        if state != self._state:
            self._state = state
            self.tray.setIcon(self._icons[state])
        if rec.phase == Phase.recording:
            tip = "Recording %s · %s" % (rec.mode.label.lower(), clock_string(rec.elapsed))
            if rec.mode == RecordMode.tab and (rec.tab_title or rec.tab_url):
                tip += "\n%s\n%s" % (rec.tab_title, rec.tab_url)
            elif rec.mode == RecordMode.window and rec.source:
                tip += "\n" + rec.source
            elif rec.mode == RecordMode.audio and rec.recording_path:
                tip += "\n" + rec.where_label
            tip += "\n" + data_summary(rec.data_lines)[0]
        elif rec.phase == Phase.finishing:
            tip = "%s (%s)" % (rec.mode.packing_label, clock_string(rec.finishing_elapsed))
        else:
            tip = "Recgo · " + data_summary(AppSettings.shared().data_lines())[0]
        self.tray.setToolTip(tip)
        if rec.is_busy:
            self._update_header()

    def _update_header(self):
        rec = Recorder.shared()
        if not hasattr(self, "_header") or self._header is None:
            return
        if rec.phase == Phase.recording:
            text = "Recording %s · %s" % (rec.mode.label.lower(), clock_string(rec.elapsed)) \
                if AppSettings.shared().menuTimer else "Recording %s" % rec.mode.label.lower()
        else:
            text = "%s · %s" % (rec.mode.packing_label, clock_string(rec.finishing_elapsed))
        self._header.setText(text)
        if self._status is not None:
            self._status.setText(rec.finishing_status or "packing…")
            self._status.setVisible(bool(rec.finishing_status))
        if self._force is not None:
            self._force.setVisible(rec.finishing_elapsed > 8)

    def _add(self, text, slot=None, enabled=True, shortcut="", checkable=False, checked=False,
             tip="", icon_name=None):
        a = QAction(text, self.menu)
        a.setEnabled(enabled)
        if icon_name:
            a.setIcon(icon(icon_name))
        if shortcut:
            a.setShortcut(QKeySequence.fromString(shortcut, QKeySequence.SequenceFormat.NativeText))
            a.setShortcutContext(Qt.ShortcutContext.WidgetShortcut)
        if checkable:
            a.setCheckable(True)
            a.setChecked(checked)
        if tip:
            a.setToolTip(tip)
            a.setStatusTip(tip)
        if slot:
            a.triggered.connect(lambda *_: slot())
        self.menu.addAction(a)
        return a

    def _fact(self, text, tip=""):
        """A quiet status line: plain text the person can read, not a badge."""
        return self._add("   " + text, enabled=False, tip=tip)

    def _data_rows(self, lines):
        for text, leaves in lines:
            a = self._fact(text, tip="Where this session's bytes go" if leaves else "")
            if leaves:
                a.setIcon(icon("leaves"))

    def _rebuild(self):
        self.menu.clear()
        self._header = None
        self._status = None
        self._force = None
        rec = Recorder.shared()
        s = AppSettings.shared()
        cdp = CDP.shared().available
        if rec.phase == Phase.recording:
            self._header = self._add("", enabled=False)
            if rec.mode == RecordMode.tab and (rec.tab_title or rec.tab_url):
                self._fact((rec.tab_title or rec.tab_url)[:64], tip=rec.tab_url)
                self._add("   Copy tab URL", lambda: QGuiApplication.clipboard().setText(rec.tab_url),
                          tip=rec.tab_url)
            elif rec.mode == RecordMode.window:
                self._fact(rec.source or "picking the screen…")
            if rec.mode == RecordMode.audio:
                self._fact("Microphone: " + (rec.mic_source or AppSettings.shared().mic_label))
                self._fact("System audio: %s (own track)" % (rec.monitor_source or "none"),
                           tip="recgo records what you hear as a track of its own, next to the microphone")
            else:
                for fact in rec.facts:
                    self._fact(fact)
            where = rec.where
            self._add("   " + rec.where_label,
                      (lambda: reveal(where) if rec.mode == RecordMode.audio else open_path(where)) if where else None,
                      enabled=bool(where), tip=where or "", icon_name="folder")
            self.menu.addSeparator()
            self._data_rows(rec.data_lines)
            self.menu.addSeparator()
            if rec.mode != RecordMode.audio:
                self._add("Mark this moment", Actions.mark, shortcut=hotkeys.label_for("shortcutMark"),
                          icon_name="mark")
            self._add("Hide HUD" if self.windows.hud_visible else "Show HUD",
                      self.windows.toggle_hud, shortcut=hotkeys.label_for("shortcutToggleHUD"), icon_name="hud")
            self._add("Hide live session" if self.windows.live_visible else "Show live session",
                      self.windows.toggle_live, icon_name="live")
            self._add("Stop" if rec.mode == RecordMode.audio else "Stop and open session", Actions.stop,
                      shortcut=hotkeys.label_for("shortcutStop"), icon_name="stop")
            self._update_header()
        elif rec.phase == Phase.finishing:
            self._header = self._add("", enabled=False)
            if rec.mode == RecordMode.audio:
                self._fact("Recording stopped. Finalizing the file, then uploading it where configured.")
            else:
                self._fact("Capture stopped. Transcribing and packing; the Library opens when done.")
            self._status = self._add("", enabled=False)
            self._force = self._add("Force stop (skips packing; raw files stay on disk)",
                                    rec.force_stop, icon_name="stop")
            self._update_header()
        else:
            for mode in RecordMode:
                enabled = not mode.needs_cdp or cdp
                text = mode.menu_text
                tip = "" if enabled else \
                    "No browser answers on 127.0.0.1:9222. Click for how to start one"
                if mode == RecordMode.window:
                    tip = "One screen or one window, chosen in the desktop's picker when the recording starts"
                if mode == RecordMode.audio and not s.captureMic:
                    enabled = False
                    tip = "Microphone capture is off, and Audio only records nothing else"
                if not enabled:
                    text += "   · why?" if mode.needs_cdp else "   · mic off"
                slot = (lambda m=mode: Actions.start(m)) if enabled else \
                    ((lambda m=mode: Actions.explain_cdp(m)) if mode.needs_cdp else None)
                self._add(text, slot,
                          enabled=enabled or mode.needs_cdp,
                          shortcut=hotkeys.label_for(mode.shortcut_key) if enabled else "",
                          tip=tip, icon_name=mode.value)
            if not cdp:
                self._fact("Browser and Tab need Chromium started with --remote-debugging-port=9222")
        self.menu.addSeparator()
        if rec.phase == Phase.idle:
            self._add("Microphone  ·  %s" % s.mic_label,
                      lambda: s.set("captureMic", not s.captureMic), checkable=True,
                      checked=s.captureMic, tip="The narration track; the device is picked in Settings → Audio")
            self._add("System audio  ·  %s" % s.monitor_label, Actions.system_audio, checkable=True,
                      checked=s.systemAudio,
                      tip="Mixed into the narration track; the source is picked in Settings → Audio")
            self.menu.addSeparator()
            self._data_rows(s.data_lines())
            self.menu.addSeparator()
        elif rec.phase == Phase.recording and rec.mode != RecordMode.audio:
            live = rec.system_audio_available
            self._add("System audio  ·  %s" % (s.monitor_label if live else "not started with it"),
                      Actions.system_audio, checkable=True, checked=live and rec.system_audio, enabled=live,
                      tip="Mutes or unmutes %s in the running recording" % rec.system_audio_source if live else
                      "Turn System audio on before starting to mix what you hear")
            self.menu.addSeparator()
        last = rec.finished_session_dir
        if last and rec.phase == Phase.idle:
            if rec.finished_is_file:
                self._add("Last recording: %s" % os.path.basename(last)[:48], lambda: reveal(last),
                          tip=last + ("\nuploaded to " + rec.uploaded if rec.uploaded else ""), icon_name="folder")
            else:
                self._add("Last session: %s" % os.path.basename(last)[:48], lambda: open_path(last),
                          tip=last, icon_name="folder")
        self._add("Library…", self.windows.show_library,
                  shortcut=hotkeys.label_for("shortcutOpenLibrary"), icon_name="library")
        self._add("Settings…", self.windows.show_settings, icon_name="settings")
        self._add("Quit Recgo", self.quit_cb, icon_name="quit")

    def _activated(self, reason):
        if reason == QSystemTrayIcon.ActivationReason.MiddleClick:
            Actions.mark()
            return
        if reason in (QSystemTrayIcon.ActivationReason.Trigger,
                      QSystemTrayIcon.ActivationReason.DoubleClick):
            self.windows.show_library()

    def notify(self, title, body):
        self.tray.showMessage(title, body, self._icons["idle"], 6000)

    def hide(self):
        self.tray.hide()
