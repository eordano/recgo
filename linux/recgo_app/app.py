import os
import sys

from PySide6.QtCore import QTimer, Qt
from PySide6.QtGui import QIcon
from PySide6.QtNetwork import QLocalServer, QLocalSocket
from PySide6.QtWidgets import QApplication, QMessageBox

from recgo_app import APP_ID, APP_NAME
from recgo_app.hotkeys import HotkeyAction, Hotkeys
from recgo_app.meet import MeetWatcher
from recgo_app.recorder import Phase, Recorder
from recgo_app.settings import AppSettings, RecordMode
from recgo_app.theme import apply_dark
from recgo_app.tray import Actions, TrayController
from recgo_app.widgets import app_icon_pixmap
from recgo_app.windows import Windows

USAGE = """usage: recgo-app [--library | --settings | --record [screen|window|browser|tab|audio [name]] | --mark | --stop
                 | --system-audio [on|off|toggle] | --toggle-hud | --toggle-live | --close | --status
                 | --quit | --shoot DIR]

With no verb the tray icon starts (or, if Recgo is already running, the Library opens).
Verbs are forwarded to the running instance, so a KDE custom shortcut or a script can
drive it: `recgo-app --record screen`, `recgo-app --mark`, `recgo-app --stop`.
--system-audio flips the setting while idle and mutes/unmutes the mix while recording.
"""

VERBS = ("--library", "--settings", "--record", "--mark", "--stop", "--system-audio", "--toggle-hud",
         "--toggle-live", "--close", "--status", "--quit")


def socket_name():
    return "recgo-app-%d" % os.getuid()


class App:
    def __init__(self, qapp):
        self.qapp = qapp
        self.windows = Windows.shared()
        self.rec = Recorder.shared()
        self.close_after_stop = False
        self.quitting = False
        self.rec.started.connect(self._on_started)
        self.rec.finished.connect(self._on_finished)
        self.meet = MeetWatcher(self.rec)
        self.tray = TrayController(self.windows, self.quit)
        Hotkeys.shared().define([
            HotkeyAction("Record screen", "shortcutRecordScreen", lambda: Actions.start(RecordMode.screen)),
            HotkeyAction("Record one screen", "shortcutRecordWindow", lambda: Actions.start(RecordMode.window)),
            HotkeyAction("Record browser", "shortcutRecordBrowser", lambda: Actions.start(RecordMode.browser)),
            HotkeyAction("Record this tab", "shortcutRecordTab", lambda: Actions.start(RecordMode.tab)),
            HotkeyAction("Record audio only", "shortcutRecordAudio", lambda: Actions.start(RecordMode.audio)),
            HotkeyAction("Mark this moment", "shortcutMark", Actions.mark),
            HotkeyAction("Hide or show the HUD", "shortcutToggleHUD", self.windows.toggle_hud),
            HotkeyAction("Stop and open session", "shortcutStop", Actions.stop),
            HotkeyAction("Open Library", "shortcutOpenLibrary", self.windows.show_library),
        ])
        Hotkeys.shared().register()
        self.server = QLocalServer()
        QLocalServer.removeServer(socket_name())
        self.server.listen(socket_name())
        self.server.newConnection.connect(self._on_connection)

    def _on_connection(self):
        sock = self.server.nextPendingConnection()
        if sock is None:
            return

        def ready():
            data = bytes(sock.readAll()).decode("utf-8", "replace").strip()
            reply = self.handle(data.split())
            sock.write((reply + "\n").encode("utf-8"))
            sock.flush()
            sock.disconnectFromServer()

        if sock.bytesAvailable():
            ready()
        else:
            sock.readyRead.connect(ready)

    def status(self):
        rec = self.rec
        w = self.windows
        parts = ["meet=%s" % self.meet.status.replace(" ", "_"), "phase=%s" % rec.phase.value, "mode=%s" % rec.mode.value,
                 "elapsed=%d" % int(rec.elapsed), "marks=%d" % rec.marks,
                 "events=%d" % len(rec.live_doc.events),
                 "hud=%d" % int(w.hud_visible), "hud_collapsed=%d" % int(w.hud_collapsed),
                 "live=%d" % int(w.live_visible),
                 "library=%d" % int(w.library is not None and w.library.isVisible()),
                 "settings=%d" % int(w.settings is not None and w.settings.isVisible()),
                 "kwin_script=%d" % int(bool(w.kwin_script)),
                 "hotkeys=%s" % ",".join(sorted(Hotkeys.shared().bound)),
                 "tab_id=%s" % rec.tab_id, "tab_url=%s" % rec.tab_url, "source=%s" % rec.source,
                 "system_audio=%d" % int(rec.phase == Phase.recording and rec.system_audio_available
                                         and rec.system_audio),
                 "system_audio_source=%s" % rec.system_audio_source,
                 "last_session=%s" % (rec.finished_session_dir or ""),
                 "session_dir=%s" % rec.session_dir,
                 "data=%s" % " | ".join(t for t, _ in (rec.data_lines if rec.is_busy
                                                       else AppSettings.shared().data_lines())),
                 "last_error=%s" % rec.last_error.replace("\n", " | ")]
        return " ".join(parts)

    def handle(self, argv):
        verb = argv[0] if argv else "--library"
        if verb == "--status":
            return self.status()
        if verb == "--quit":
            QTimer.singleShot(0, self.quit)
        elif verb == "--library":
            self.windows.show_library()
        elif verb == "--settings":
            self.windows.show_settings()
        elif verb == "--record":
            spec = argv[1] if len(argv) > 1 else AppSettings.shared().default_mode.value
            try:
                Actions.start(RecordMode(spec), name=argv[2] if len(argv) > 2 else "")
            except ValueError:
                pass
        elif verb == "--mark":
            Actions.mark()
        elif verb == "--system-audio":
            state = Actions.system_audio(argv[1] if len(argv) > 1 else "toggle")
            return "unavailable" if state is None else ("on" if state else "off")
        elif verb == "--stop":
            Actions.stop()
        elif verb == "--toggle-hud":
            self.windows.toggle_hud()
        elif verb == "--toggle-live":
            self.windows.toggle_live()
        elif verb == "--close":
            self.windows.close_auxiliary()
        return "ok"

    def _on_started(self):
        self.windows.close_auxiliary()
        self.windows.show_hud()
        if AppSettings.shared().liveOnStart:
            self.windows.show_live()

    def _on_finished(self, directory):
        self.windows.close_hud()
        self.windows.close_live()
        if self.quitting:
            self.qapp.quit()
            return
        if self.close_after_stop:
            self.close_after_stop = False
            return
        if directory and self.rec.finished_is_file:
            body = os.path.basename(directory)
            if self.rec.uploaded:
                body += "\nuploaded to " + self.rec.uploaded
            self.tray.notify("Recording saved", body)
        elif directory:
            title = os.path.basename(directory)
            self.tray.notify("Session packed", title)
            self.windows.show_library(selecting=title)
        elif self.rec.last_error:
            info = self.rec.last_error
            if ":9222" in info:
                info += ("\n\nBrowser and Tab modes attach to Chromium's debug port. Start it with "
                         "--remote-debugging-port=9222 first.")
            box = QMessageBox()
            box.setWindowTitle(APP_NAME)
            box.setText("Recording did not finish")
            box.setInformativeText(info)
            box.exec()

    def quit(self):
        rec = self.rec
        if not rec.is_busy:
            self.qapp.quit()
            return
        recording = rec.is_recording
        box = QMessageBox()
        box.setWindowTitle(APP_NAME)
        box.setText("Stop this session?" if recording else "The last session is still packing")
        box.setInformativeText("Quitting stops the recording and packs it first (up to 15 seconds, then a "
                               "force stop keeps the raw files)." if recording else
                               "Quit waits for the pack to land (or force-stops after 15 seconds).")
        stop = box.addButton("Stop and quit" if recording else "Wait, then quit", QMessageBox.ButtonRole.AcceptRole)
        box.addButton("Cancel", QMessageBox.ButtonRole.RejectRole)
        box.exec()
        if box.clickedButton() is not stop:
            return
        self.quitting = True
        if recording:
            rec.stop()

        def deadline():
            if self.quitting and rec.phase == Phase.finishing:
                rec.force_stop()
                QTimer.singleShot(3000, self.qapp.quit)

        QTimer.singleShot(15000, deadline)

    def shutdown(self):
        self.meet.shutdown()
        Hotkeys.shared().shutdown()
        self.windows.shutdown()
        self.tray.hide()
        self.server.close()


def forward(argv):
    sock = QLocalSocket()
    sock.connectToServer(socket_name())
    if not sock.waitForConnected(300):
        return False
    sock.write((" ".join(argv) + "\n").encode("utf-8"))
    sock.waitForBytesWritten(500)
    if sock.waitForReadyRead(3000):
        print(bytes(sock.readAll()).decode("utf-8", "replace").strip())
    sock.disconnectFromServer()
    return True


def shoot(qapp, out_dir):
    from recgo_app.hud import HUDWindow
    from recgo_app.library import LibraryStore, LibraryWindow
    from recgo_app.live import LiveWindow
    from recgo_app.settings_view import PANES, SettingsWindow
    os.makedirs(out_dir, exist_ok=True)
    windows = Windows.shared()
    LibraryStore.shared().reload()
    from recgo_app.tray import TrayController
    settings = SettingsWindow()
    shots = {"library": LibraryWindow(), "settings": settings, "live": LiveWindow(),
             "hud": HUDWindow(windows)}
    for name, w in shots.items():
        w.show()
    qapp.processEvents()
    QTimer.singleShot(400, lambda: None)
    qapp.processEvents()
    for name, w in shots.items():
        w.grab().save(os.path.join(out_dir, name + ".png"))
    for i, (name, _) in enumerate(PANES):
        settings.select(i)
        qapp.processEvents()
        settings.grab().save(os.path.join(out_dir, "settings-%s.png" % name.split(" ")[0].lower()))
    for w in shots.values():
        w.close()
    tray = TrayController(windows, lambda: None)
    tray._rebuild()
    tray.menu.show()
    qapp.processEvents()
    tray.menu.grab().save(os.path.join(out_dir, "menu.png"))
    tray.menu.hide()
    tray.hide()
    return 0


def main(argv):
    args = argv[1:]
    if args and args[0] in ("-h", "--help"):
        print(USAGE, end="")
        return 0
    if args and args[0] == "--shoot":
        qapp = QApplication(argv)
        apply_dark(qapp)
        return shoot(qapp, args[1] if len(args) > 1 else "shots")
    qapp = QApplication(argv)
    qapp.setApplicationName("recgo")
    qapp.setApplicationDisplayName(APP_NAME)
    qapp.setDesktopFileName(APP_ID)
    qapp.setWindowIcon(QIcon(app_icon_pixmap()))
    qapp.setQuitOnLastWindowClosed(False)
    apply_dark(qapp)
    if forward(args or ["--library"]):
        return 0
    if args and args[0] not in VERBS:
        print(USAGE, end="", file=sys.stderr)
        return 2
    if args and args[0] in ("--mark", "--stop", "--system-audio", "--toggle-hud", "--toggle-live", "--close",
                            "--status", "--quit"):
        print("recgo-app is not running", file=sys.stderr)
        return 1
    app = App(qapp)
    if args:
        QTimer.singleShot(0, lambda: app.handle(args))
    qapp.aboutToQuit.connect(app.shutdown)
    return qapp.exec()


_ = Qt
