import os
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

from types import SimpleNamespace

from PySide6.QtDBus import QDBusMessage
from PySide6.QtWidgets import QApplication

from recgo_app import tray


def test_saved_notification_buttons_act_on_its_path(monkeypatch):
    QApplication.instance() or QApplication([])
    sent, done = [], []
    reply = SimpleNamespace(returncode=0, stdout="u 7\n")
    monkeypatch.setattr(tray.subprocess, "run", lambda argv, **kw: sent.append(argv) or reply)
    monkeypatch.setattr(tray, "reveal", lambda p: done.append(("reveal", p)))
    monkeypatch.setattr(tray, "copy_text", lambda p: done.append(("copy", p)))
    ui = SimpleNamespace(_notified={}, _icons={"idle": None},
                         tray=SimpleNamespace(showMessage=lambda *a: done.append("popup")),
                         windows=SimpleNamespace(show_library=lambda: done.append("library")))
    tray.TrayController.notify(ui, "Recording saved", "a & b.mkv", path="/r/a & b.mkv")
    assert "a &amp; b.mkv" in sent[0] and "Show in folder" in sent[0] and "Copy path" in sent[0]
    for nid, action in ((7, "reveal"), (7, "copy"), (7, "default"), (8, "copy")):
        msg = QDBusMessage.createSignal("/org/freedesktop/Notifications", tray.NOTIFICATIONS, "ActionInvoked")
        msg.setArguments([nid, action])
        tray.TrayController._notification_action(ui, msg)
    assert done == [("reveal", "/r/a & b.mkv"), ("copy", "/r/a & b.mkv"), "library"]
    reply.returncode = 1
    tray.TrayController.notify(ui, "Recording saved", "x.mkv", path="/r/x.mkv")
    assert done[-1] == "popup"
