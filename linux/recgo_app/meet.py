import json
import time

from PySide6.QtCore import QObject, QProcess, QTimer, Qt
from PySide6.QtWidgets import QLineEdit, QMessageBox

from recgo_app.recorder import Phase, recording_name
from recgo_app.settings import AppSettings, RecordMode


def suggested_name(call):
    """The recording name to offer for a call: the detector's title (the
    meeting's name, else meet-<room code>), cleaned the way recgo names files."""
    title = call.get("title") or "meet-" + call.get("url", "").rstrip("/").rsplit("/", 1)[-1]
    return recording_name(title, meeting=True)


class MeetWatcher(QObject):
    """Prompt once per call; own only recordings accepted through this prompt."""
    def __init__(self, recorder):
        super().__init__()
        self.rec = recorder
        self.proc = None
        self.buffer = b""
        self.calls = {}
        self.prompted = set()
        self.owned = None
        self.dialog = None
        self.dialog_id = None
        self.last_update = 0
        self.status = "disabled"
        self.closed = False
        self.rec.phase_changed.connect(self.phase_changed)
        self.timer = QTimer(self)
        self.timer.setInterval(2000)
        self.timer.timeout.connect(self.refresh)
        self.timer.start()
        self.refresh()

    def refresh(self):
        if self.closed:
            return
        if not AppSettings.shared().meetPrompt:
            self.shutdown_process()
            self.status = "disabled"
            return
        if self.proc is None:
            binary = AppSettings.shared().resolve_binary("recgo-meet-watch")
            if not binary:
                self.status = "helper missing"
                return
            p = QProcess(self)
            p.setProgram(binary)
            p.readyReadStandardOutput.connect(self.read)
            p.finished.connect(lambda *_: self.process_ended(p))
            p.errorOccurred.connect(lambda *_: self.process_ended(p))
            self.proc = p
            self.buffer = b""
            self.status = "connecting"
            p.start()
        if time.monotonic() - self.last_update > 8:
            self.close_prompt()
            self.calls = {}
            self.status = "browser unavailable"

    def read(self):
        if self.proc is None:
            return
        self.buffer += bytes(self.proc.readAllStandardOutput())
        while b"\n" in self.buffer:
            line, self.buffer = self.buffer.split(b"\n", 1)
            try:
                self.update(json.loads(line))
            except (ValueError, KeyError, TypeError):
                self.status = "invalid detector response"

    def update(self, snapshot):
        if not AppSettings.shared().meetPrompt:
            self.close_prompt()
            return
        self.last_update = time.monotonic()
        self.status = "watching" if snapshot["available"] else "browser unavailable"
        calls = {c["id"]: c for c in snapshot["calls"]}
        self.calls = calls if snapshot["available"] else {}
        # Unknown keeps consent and ownership, but never permits a stale prompt.
        if snapshot["available"]:
            self.prompted.intersection_update(calls)
            if self.owned is not None and self.owned not in calls:
                self.owned = None
                self.rec.stop()
        if self.dialog_id not in self.calls or not self.calls.get(self.dialog_id, {}).get("present"):
            self.close_prompt()
        if self.rec.is_busy or self.dialog is not None or not snapshot["available"]:
            return
        for call in calls.values():
            if call["present"] and call["id"] not in self.prompted:
                self.prompted.add(call["id"])
                self.show_prompt(call)
                break

    def show_prompt(self, call):
        box = QMessageBox()
        box.setWindowTitle("Recgo — Google Meet")
        box.setText("Record this Meet call?")
        box.setInformativeText(call["url"] + "\n\nRecords your microphone and system audio as "
                               "`recgo <name>` does, then stops when you leave. Uses your transcription "
                               "and sharing settings.\n\nRecording name:")
        edit = QLineEdit(suggested_name(call))
        edit.setPlaceholderText("meet")
        edit.selectAll()
        lay = box.layout()
        lay.addWidget(edit, lay.rowCount(), 0, 1, lay.columnCount())
        box.name_edit = edit
        box.setStandardButtons(QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.No)
        box.button(QMessageBox.StandardButton.Yes).setText("Record call")
        box.button(QMessageBox.StandardButton.No).setText("Not now")
        box.setDefaultButton(QMessageBox.StandardButton.No)
        box.setEscapeButton(QMessageBox.StandardButton.No)
        box.setWindowModality(Qt.WindowModality.NonModal)
        self.dialog = box
        self.dialog_id = call["id"]
        box.finished.connect(lambda result: self.answer(call["id"], box, result))
        box.show()
        box.raise_()

    def answer(self, call_id, box, result):
        if self.dialog is not box:
            return
        self.dialog = None
        self.dialog_id = None
        box.deleteLater()
        if (result == QMessageBox.StandardButton.Yes and not self.rec.is_busy
                and AppSettings.shared().meetPrompt
                and time.monotonic() - self.last_update < 8
                and self.calls.get(call_id, {}).get("present")):
            self.rec.start(RecordMode.audio, meeting=True, name=box.name_edit.text())
            if self.rec.is_recording:
                self.owned = call_id

    def close_prompt(self):
        box = self.dialog
        self.dialog = None
        self.dialog_id = None
        if box is not None:
            box.close()
            box.deleteLater()

    def phase_changed(self, phase):
        if phase != Phase.idle.value:
            self.close_prompt()
        if phase != Phase.recording.value:
            self.owned = None

    def process_ended(self, process):
        if self.proc is process:
            self.proc = None
            self.calls = {}
            self.close_prompt()
            self.status = "detector stopped"
        process.deleteLater()

    def shutdown_process(self):
        self.close_prompt()
        self.calls = {}
        # Disabling detection hands any running recording back to manual control.
        self.owned = None
        if self.proc is not None:
            p, self.proc = self.proc, None
            p.terminate()
            if not p.waitForFinished(1000):
                p.kill()
                p.waitForFinished(1000)

    def shutdown(self):
        self.closed = True
        self.timer.stop()
        self.shutdown_process()
