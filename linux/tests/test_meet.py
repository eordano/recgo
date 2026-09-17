import os
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

import pytest
from PySide6.QtCore import QObject, Signal
from PySide6.QtWidgets import QApplication, QMessageBox
from recgo_app.meet import MeetWatcher
from recgo_app.settings import AppSettings


class FakeRecorder(QObject):
    phase_changed = Signal(str)
    is_busy = False
    is_recording = False

    def __init__(self):
        super().__init__()
        self.starts = []
        self.stops = 0

    def start(self, mode, **kwargs):
        self.starts.append((mode, kwargs))
        self.is_busy = self.is_recording = True
        self.phase_changed.emit("recording")

    def stop(self):
        self.stops += 1
        self.is_busy = True
        self.is_recording = False
        self.phase_changed.emit("finishing")


@pytest.fixture
def watcher(monkeypatch):
    app = QApplication.instance() or QApplication([])
    class Settings:
        meetPrompt = False
        def resolve_binary(self, name):
            return None
    s = Settings()
    monkeypatch.setattr(AppSettings, "_instance", s)
    w = MeetWatcher(FakeRecorder())
    w.timer.stop()
    s.meetPrompt = True
    yield w
    w.shutdown()
    app.processEvents()


def snapshot(present=True, available=True, empty=False, title=None):
    call = {"id": "one", "url": "https://meet.google.com/abc-defg-hij", "present": present}
    if title is not None:
        call["title"] = title
    return {"available": available, "calls": [] if empty else [call]}


def test_prompt_is_default_no_and_dismissal_is_once_per_call(watcher):
    w = watcher
    w.update(snapshot())
    assert w.dialog.defaultButton() == w.dialog.button(QMessageBox.StandardButton.No)
    w.dialog.button(QMessageBox.StandardButton.No).click()
    w.update(snapshot())
    assert w.dialog is None and not w.rec.starts
    w.update(snapshot(empty=True))
    w.update(snapshot())
    assert w.dialog is not None


def test_accept_then_leave_stops_only_owned_recording(watcher):
    w = watcher
    w.update(snapshot())
    w.dialog.button(QMessageBox.StandardButton.Yes).click()
    assert w.rec.starts[0][1] == {"meeting": True, "name": "meet-abc-defg-hij"}
    assert w.owned == "one"
    w.update(snapshot(present=False))
    assert w.rec.stops == 0
    w.update(snapshot(empty=True))
    assert w.rec.stops == 1


def test_manual_stop_does_not_reprompt_or_stop_next_manual_recording(watcher):
    w = watcher
    w.update(snapshot())
    w.dialog.button(QMessageBox.StandardButton.Yes).click()
    w.rec.stop()
    w.rec.is_busy = w.rec.is_recording = False
    w.rec.phase_changed.emit("idle")
    w.update(snapshot())
    assert w.dialog is None
    w.rec.start("audio")
    w.update(snapshot(empty=True))
    assert w.rec.stops == 1


def test_unknown_closes_stale_prompt_and_preserves_owned_session(watcher):
    w = watcher
    w.update(snapshot())
    old = w.dialog
    w.update(snapshot(available=False))
    w.answer("one", old, QMessageBox.StandardButton.Yes)
    assert not w.rec.starts and w.dialog is None
    w.update(snapshot())
    assert w.dialog is None
    w.owned = "one"
    w.update(snapshot(available=False))
    assert w.owned == "one" and w.rec.stops == 0


def test_busy_and_manual_start_while_prompt_is_open(watcher):
    w = watcher
    w.rec.is_busy = True
    w.update(snapshot())
    assert w.dialog is None
    w.rec.is_busy = False
    w.update(snapshot())
    old = w.dialog
    w.rec.start("audio")
    assert w.dialog is None
    w.answer("one", old, QMessageBox.StandardButton.Yes)
    assert len(w.rec.starts) == 1 and w.owned is None


def test_leaving_before_accepting_cannot_start(watcher):
    w = watcher
    w.update(snapshot())
    old = w.dialog
    w.update(snapshot(present=False))
    w.answer("one", old, QMessageBox.StandardButton.Yes)
    assert not w.rec.starts


def test_prompt_suggests_the_meeting_title_and_takes_an_edited_one(watcher):
    from recgo_app.meet import suggested_name
    assert suggested_name({"url": "https://meet.google.com/abc-defg-hij"}) == "meet-abc-defg-hij"
    assert suggested_name({"url": "https://meet.google.com/abc-defg-hij", "title": "Weekly sync"}) == "Weekly-sync"
    w = watcher
    w.update(snapshot(title="Weekly sync"))
    assert w.dialog.name_edit.text() == "Weekly-sync"
    w.dialog.name_edit.setText("q3 planning")
    w.dialog.button(QMessageBox.StandardButton.Yes).click()
    assert w.rec.starts[0] == ("audio", {"meeting": True, "name": "q3 planning"})
