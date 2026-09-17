import os
import random
import re
import signal
import time
from enum import Enum

from PySide6.QtCore import QObject, QProcess, QProcessEnvironment, QTimer, Signal

from recgo_app.cdp import CDP
from recgo_app.model import EventKind, SessionDoc, SessionEvent, clock_seconds, parse_session_doc
from recgo_app.settings import AppSettings, RecordMode, extra_path_dirs

TRANSCRIPT_RE = re.compile(r"^(\d\d:\d\d:\d\d)  (narration|system): (.*)$")


class Phase(str, Enum):
    idle = "idle"
    recording = "recording"
    finishing = "finishing"


def recording_name(name, meeting=False):
    """The <name> handed to `recgo <name>`: what the person typed, cleaned
    the way the CLI's own filenames come out, else meet / audio."""
    name = re.sub(r"[^A-Za-z0-9._-]+", "-", (name or "").strip()).strip("-.")
    return name or ("meet" if meeting else "audio")


def finished_path(lines):
    """The `wrote ...` line the recorders end on: a session folder when it
    names SESSION.md, the recording file itself for `recgo`."""
    wrote = next((l for l in reversed(lines) if l.startswith("wrote ")), None)
    if not wrote:
        return None
    path = wrote[len("wrote "):].strip()
    if path.endswith("/SESSION.md"):
        return path[:-len("/SESSION.md")]
    return path


def uploaded_to(lines):
    up = next((l for l in reversed(lines) if l.startswith("Uploaded to ")), None)
    return up[len("Uploaded to "):].strip() if up else ""


def mint_room():
    a = ["walk", "amber", "quiet", "brisk", "misty", "solar", "cedar", "ember"]
    b = ["lantern", "harbor", "meadow", "signal", "orbit", "thicket", "compass", "quarry"]
    suffix = "".join(random.choice("23456789abcdefghjkmnpqrstuvwxyz") for _ in range(4))
    return "%s-%s-%s" % (random.choice(a), random.choice(b), suffix)


class Recorder(QObject):
    phase_changed = Signal(str)
    tick = Signal()
    live_changed = Signal()
    started = Signal()
    finished = Signal(object)
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        self.phase = Phase.idle
        self.mode = RecordMode.screen
        self.elapsed = 0.0
        self.finishing_elapsed = 0.0
        self.finishing_status = ""
        self.live_doc = SessionDoc()
        self.last_narration = ""
        self.hearing = ""
        self.marks = 0
        self.last_error = ""
        self.finished_session_dir = None
        self.uploaded = ""
        self.active_room = ""
        self.tab_id = ""
        self.tab_title = ""
        self.tab_url = ""
        self.source = ""
        self.system_audio_source = ""
        self.system_audio = False
        self.name = ""
        self.recording_path = ""
        self.mic_source = ""
        self.monitor_source = ""
        self.facts = []
        self.data_lines = []
        self._target = None
        self._meeting = False
        self._proc = None
        self._stderr_tail = []
        self._start = None
        self._stop = None
        self._live_dir = None
        self._timer = QTimer(self)
        self._timer.setInterval(500)
        self._timer.timeout.connect(self._pulse)

    @property
    def is_recording(self):
        return self.phase == Phase.recording

    @property
    def is_busy(self):
        return self.phase != Phase.idle

    def _set_phase(self, phase):
        self.phase = phase
        self.phase_changed.emit(phase.value)

    @property
    def system_audio_available(self):
        return self.phase == Phase.recording and bool(self.system_audio_source)

    @property
    def session_dir(self):
        return self._live_dir or ""

    @property
    def where(self):
        """The thing being written: the session folder, or for Audio only
        the recording file `recgo` announced."""
        if self.mode == RecordMode.audio:
            return self.recording_path
        return self._live_dir or ""

    @property
    def where_label(self):
        w = self.where
        if w:
            return os.path.basename(w.rstrip("/"))
        return "starting the recorder…" if self.mode == RecordMode.audio else "creating the session folder…"

    @property
    def finished_is_file(self):
        return bool(self.finished_session_dir) and os.path.isfile(self.finished_session_dir)

    # What this session captures and where its bytes go, fixed at start:
    # a setting changed mid-session describes the next one, not this one.
    def _freeze_facts(self, mode):
        s = AppSettings.shared()
        facts = []
        if mode == RecordMode.audio:
            facts.append("Microphone: " + s.mic_label)
            facts.append("System audio: %s (own track)" % s.monitor_label)
            self.facts = facts
            self.data_lines = s.audio_data_lines()
            return
        if s.captureMic:
            facts.append("Microphone: " + s.mic_label)
            if s.systemAudio or self._meeting:
                facts.append("System audio: " + s.monitor_label)
        else:
            facts.append("No microphone")
        if mode.captures_screen:
            shots = [n for n, on in (("clicks", s.clickShots), ("focus changes", s.focusShots)) if on]
            facts.append("Screenshots on " + " and ".join(shots) if shots else "No click or focus screenshots")
        self.facts = facts
        self.data_lines = s.data_lines()

    def start(self, mode, target=None, meeting=False, name=""):
        if self.phase != Phase.idle:
            return
        if mode == RecordMode.audio and not AppSettings.shared().captureMic:
            self.last_error = ("Audio-only records the microphone, and microphone capture "
                               "is switched off — re-enable it in the tray menu or Settings first")
            self.finished.emit(None)
            return
        self._meeting = meeting
        self.mode = mode
        self.name = recording_name(name, meeting)
        self.last_error = ""
        self.finished_session_dir = None
        self.uploaded = ""
        self._freeze_facts(mode)
        target = target if mode == RecordMode.tab else None
        self.tab_id = (target or {}).get("id", "")
        self.tab_title = (target or {}).get("title", "")
        self.tab_url = (target or {}).get("url", "")
        self._target = target
        self._spawn(mode)

    def build_arguments(self, mode, target=None):
        s = AppSettings.shared()
        if mode == RecordMode.audio:
            # `recgo <name>` as typed in a terminal: devices, output folder,
            # max duration and upload all come from recgo's own config.toml;
            # the app only pins a device it was told to and keeps its own
            # Never upload promise.
            args = ["-headless"]
            if s.micDevice:
                args += ["-mic", s.micDevice]
            if s.monitorDevice:
                args += ["-system-audio", s.monitorDevice]
            if s.audio_transcribe:
                args.append("-transcribe")
            if s.neverUpload:
                args.append("-no-upload")
            args.append(self.name or "audio")
            return args
        args = ["-out", s.out_root]
        if mode == RecordMode.tab and target and target.get("id"):
            args += ["-target", target["id"]]
        audio = s.captureMic
        if not s.captureMic:
            args.append("-no-audio")
        if s.micDevice:
            args += ["-mic", s.micDevice]
        if self._meeting:
            args.append("-require-system-audio")
        if audio and (s.systemAudio or self._meeting):
            args += ["-system-audio", s.monitorDevice or "default"]
        args += ["-stt-backend", s.effective_stt_backend]
        if s.sttLanguage:
            args += ["-stt-language", s.sttLanguage]
        if s.whisperModel:
            args += ["-whisper-model", os.path.expanduser(s.whisperModel)]
        if s.whisperBin:
            args += ["-whisper-bin", os.path.expanduser(s.whisperBin)]
        if not s.autoTitle:
            args += ["-title-backend", "none"]
        if mode.captures_screen:
            args.append("-click-shots=%s" % str(s.clickShots).lower())
            args.append("-focus-shots=%s" % str(s.focusShots).lower())
        if s.portal_active:
            self.active_room = s.portalRoom or mint_room()
            args += ["-portal", s.portalURL, "-portal-room", self.active_room]
        else:
            self.active_room = ""
        if s.sync_active:
            args += ["-sync-target", s.syncTarget]
            if s.syncKey:
                args += ["-sync-key", os.path.expanduser(s.syncKey)]
        else:
            args.append("-no-sync")
        return args

    def _spawn(self, mode):
        binary = AppSettings.shared().resolve_binary(mode.binary)
        if not binary:
            self.last_error = "%s not found — set the binaries folder in Settings" % mode.binary
            self.finished.emit(None)
            return
        p = QProcess(self)
        p.setProgram(binary)
        p.setArguments(self.build_arguments(mode, self._target))
        p.setWorkingDirectory(os.path.expanduser("~"))
        env = QProcessEnvironment.systemEnvironment()
        path = [d for d in env.value("PATH", "/usr/bin:/bin").split(":") if d]
        for extra in extra_path_dirs():
            if extra not in path:
                path.append(extra)
        env.insert("PATH", ":".join(path))
        p.setProcessEnvironment(env)
        p.readyReadStandardError.connect(self._read_stderr)
        p.finished.connect(self._process_ended)
        self._stderr_tail = []
        p.start()
        if not p.waitForStarted(3000):
            self.last_error = "could not start %s: %s" % (mode.binary, p.errorString())
            self.finished.emit(None)
            return
        self._proc = p
        self._start = time.monotonic()
        self._stop = None
        self.elapsed = 0.0
        self.marks = 0
        self.live_doc = SessionDoc()
        self.last_narration = ""
        self.hearing = ""
        self.source = ""
        self.system_audio_source = ""
        self.system_audio = False
        self.recording_path = ""
        self.mic_source = ""
        self.monitor_source = ""
        self._live_dir = None
        self._set_phase(Phase.recording)
        self.started.emit()
        self._timer.start()

    def _pulse(self):
        if self._start is None:
            return
        if self.phase == Phase.finishing:
            if self._stop is not None:
                self.finishing_elapsed = time.monotonic() - self._stop
            self.tick.emit()
            return
        if self.phase != Phase.recording:
            return
        self.elapsed = time.monotonic() - self._start
        if self.mode == RecordMode.audio:
            self.tick.emit()
            return
        if self._live_dir is None and self._proc is not None:
            root = AppSettings.shared().out_root
            suffix = "-recording-%d" % self._proc.processId()
            try:
                for name in os.listdir(root):
                    if name.endswith(suffix):
                        self._live_dir = os.path.join(root, name)
                        break
            except OSError:
                pass
        if self._live_dir:
            try:
                with open(os.path.join(self._live_dir, "SESSION.live.md"), encoding="utf-8") as fh:
                    text = fh.read()
            except OSError:
                text = None
            if text is not None:
                doc = parse_session_doc(text)
                if self.tab_id and not self.tab_url:
                    self.tab_url = doc.page
                changed = (len(doc.events) != len(self.live_doc.events)
                           or (doc.events and self.live_doc.events
                               and doc.events[-1].text != self.live_doc.events[-1].text))
                self.live_doc = doc
                for ev in reversed(doc.events):
                    if ev.kind == EventKind.narration:
                        if ev.text != self.last_narration:
                            self.last_narration = ev.text
                            self.hearing = ""
                        break
                if changed:
                    self.live_changed.emit()
        if self.tab_id:
            self._follow_tab()
        self.tick.emit()

    # The attached tab is looked up by id on every pulse: the title and URL
    # keep moving under the recorder (navigation, a SPA's own title), and the
    # browser's own list is the only place both are current.
    def _follow_tab(self):
        t = CDP.shared().tab(self.tab_id)
        if t is None:
            for ev in reversed(self.live_doc.events):
                if ev.kind == EventKind.sys and ev.text.startswith("Navigate: "):
                    self.tab_url = ev.text[len("Navigate: "):]
                    break
            return
        self.tab_title = t["title"] or self.tab_title
        self.tab_url = t["url"] or self.tab_url

    def _read_stderr(self):
        if self._proc is None:
            return
        data = bytes(self._proc.readAllStandardError()).decode("utf-8", "replace")
        for line in data.split("\n"):
            if line:
                self._stderr_tail.append(line)
                self._ingest(line)
        if len(self._stderr_tail) > 60:
            del self._stderr_tail[: len(self._stderr_tail) - 60]
        if self.phase == Phase.finishing:
            for line in reversed(self._stderr_tail):
                if line.strip():
                    self.finishing_status = line.strip()
                    break
            self.tick.emit()

    def _ingest(self, line):
        if self.phase != Phase.recording:
            return
        if self.mode == RecordMode.audio and self._ingest_audio(line):
            self.tick.emit()
            return
        if "  hearing: " in line:
            self.hearing = line.split("  hearing: ", 1)[1].strip()
        elif line.startswith("tab: "):
            parts = line[len("tab: "):].split(" ", 1)
            self.tab_id = parts[0]
            if len(parts) > 1 and parts[1].strip():
                self.tab_url = parts[1].strip()
        elif line.startswith("attached to: "):
            self.tab_title = line[len("attached to: "):].strip()
        elif line.startswith("capturing "):
            self.source = line[len("capturing "):].strip()
        elif line.startswith("audio: system audio mixed in from "):
            self.system_audio_source = line[len("audio: system audio mixed in from "):].strip()
            self.system_audio = True
        elif line in ("system audio: on", "system audio: off"):
            self.system_audio = line.endswith(" on")
        elif line.startswith("system audio: "):
            self.system_audio_source = ""
            self.system_audio = False
        else:
            return
        self.tick.emit()

    # `recgo -headless` says what it is doing one line at a time; the
    # transcript lines become the live document, since there is no
    # SESSION.live.md to tail.
    def _ingest_audio(self, line):
        if line.startswith("recording -> "):
            self.recording_path = line[len("recording -> "):].strip()
        elif line.startswith("mic: "):
            self.mic_source = line[len("mic: "):].strip()
        elif line.startswith("monitor: "):
            src = line[len("monitor: "):].strip()
            self.monitor_source = "" if src == "none" else src
        elif line.startswith("error: "):
            self.last_error = line[len("error: "):].strip()
        else:
            m = TRANSCRIPT_RE.match(line)
            if not m:
                return False
            kind = EventKind.narration if m.group(2) == "narration" else EventKind.sys
            text = m.group(3).strip() if kind == EventKind.narration else "system: " + m.group(3).strip()
            self.live_doc.events.append(SessionEvent(clock_seconds(m.group(1).replace(":", ".")), kind, text))
            if kind == EventKind.narration:
                self.last_narration = text
                self.hearing = ""
            self.live_changed.emit()
        return True

    def mark(self):
        if self.phase != Phase.recording or self._proc is None or self.mode == RecordMode.audio:
            return
        self._proc.write(b"m\n")
        self.marks += 1
        self.tick.emit()

    def toggle_system_audio(self):
        """Mute or unmute the monitor mix in the running recorder. Returns the
        state asked for; the recorder's own `system audio: on|off` line confirms it."""
        if not self.system_audio_available or self._proc is None:
            return None
        on = not self.system_audio
        self._proc.write(b"sysaudio on\n" if on else b"sysaudio off\n")
        self.system_audio = on
        self.tick.emit()
        return on

    def stop(self):
        if self.phase != Phase.recording or self._proc is None:
            return
        self._stop = time.monotonic()
        self.finishing_elapsed = 0.0
        self.finishing_status = "stopping..."
        self._set_phase(Phase.finishing)
        os.kill(self._proc.processId(), signal.SIGINT)

    def force_stop(self):
        if self.phase != Phase.finishing or self._proc is None:
            return
        p = self._proc
        self.finishing_status = "force stopping..."
        pid = p.processId()
        os.kill(pid, signal.SIGINT)

        def hard():
            if self._proc is p and self.phase == Phase.finishing:
                try:
                    os.kill(pid, signal.SIGKILL)
                except OSError:
                    pass

        QTimer.singleShot(2000, hard)

    def _process_ended(self, code, status):
        self._timer.stop()
        if self._proc is not None:
            self._read_stderr()
        d = finished_path(self._stderr_tail)
        self.uploaded = uploaded_to(self._stderr_tail)
        if d is None and code != 0:
            self.last_error = "\n".join(self._stderr_tail[-4:])
        self.finished_session_dir = d
        self._proc = None
        self._start = None
        self._stop = None
        self.finishing_elapsed = 0.0
        self.finishing_status = ""
        self._live_dir = None
        self._set_phase(Phase.idle)
        self.finished.emit(d)
