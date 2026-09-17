from recgo_app.model import EventKind
from recgo_app.recorder import Phase, Recorder, finished_path, recording_name, uploaded_to
from recgo_app.settings import AppSettings, RecordMode, audio_data_lines


class _Settings:
    captureMic = True
    out_root = "/tmp/recgo-test"
    mic_label = "system default microphone"
    monitor_label = "default output"
    micDevice = ""
    systemAudio = False
    monitorDevice = ""
    effective_stt_backend = "none"
    sttLanguage = ""
    whisperModel = ""
    whisperBin = ""
    autoTitle = True
    clickShots = True
    focusShots = False
    portal_active = False
    portalRoom = ""
    portalURL = ""
    sync_active = False
    syncTarget = ""
    syncKey = ""
    neverUpload = True
    sttBackend = "auto"
    audio_transcribe = False

    def data_lines(self):
        return [("Nothing leaves this computer", False)]

    def audio_data_lines(self):
        return [("Nothing leaves this computer", False)]


def _recorder(monkeypatch, settings):
    monkeypatch.setattr(AppSettings, "_instance", settings)
    monkeypatch.setattr(Recorder, "_instance", None)
    return Recorder()


def test_audio_mode_refused_when_mic_capture_off(monkeypatch):
    s = _Settings()
    s.captureMic = False
    rec = _recorder(monkeypatch, s)
    finished = []
    rec.finished.connect(finished.append)
    spawned = []
    monkeypatch.setattr(rec, "_spawn", spawned.append)
    rec.start(RecordMode.audio)
    assert spawned == []
    assert rec.phase == Phase.idle
    assert "microphone capture" in rec.last_error
    assert finished == [None]


def test_audio_mode_starts_when_mic_capture_on(monkeypatch):
    rec = _recorder(monkeypatch, _Settings())
    spawned = []
    monkeypatch.setattr(rec, "_spawn", spawned.append)
    rec.start(RecordMode.audio)
    assert spawned == [RecordMode.audio]
    assert rec.last_error == ""
    assert rec.name == "audio"
    assert rec.facts == ["Microphone: system default microphone", "System audio: default output (own track)"]
    assert rec.data_lines == [("Nothing leaves this computer", False)]


def test_facts_are_frozen_at_start_and_say_what_is_captured(monkeypatch):
    s = _Settings()
    s.systemAudio = True
    s.focusShots = False
    rec = _recorder(monkeypatch, s)
    monkeypatch.setattr(rec, "_spawn", lambda mode: None)
    rec.start(RecordMode.window)
    assert rec.facts == ["Microphone: system default microphone", "System audio: default output",
                         "Screenshots on clicks"]
    s.captureMic = False
    s.clickShots = False
    rec.phase = Phase.idle
    rec.start(RecordMode.screen)
    assert rec.facts == ["No microphone", "No click or focus screenshots"]


def test_audio_only_is_recgo_name_run_headless(monkeypatch):
    s = _Settings()
    rec = _recorder(monkeypatch, s)
    monkeypatch.setattr(rec, "_spawn", lambda mode: None)
    rec.start(RecordMode.audio, name="Standup notes!")
    assert RecordMode.audio.binary == "recgo"
    assert rec.name == "Standup-notes"
    assert rec.build_arguments(RecordMode.audio) == ["-headless", "-no-upload", "Standup-notes"]
    s.neverUpload = False
    s.audio_transcribe = True
    s.micDevice = "alsa_input.usb"
    s.monitorDevice = "alsa_output.usb.monitor"
    args = rec.build_arguments(RecordMode.audio)
    assert args == ["-headless", "-mic", "alsa_input.usb", "-system-audio", "alsa_output.usb.monitor",
                    "-transcribe", "Standup-notes"]
    for flag in ("-out", "-no-video", "-stt-backend", "-no-sync", "-click-shots"):
        assert flag not in args


def test_recording_name_follows_recgo_filenames():
    assert recording_name("") == "audio"
    assert recording_name("", meeting=True) == "meet"
    assert recording_name("  weekly sync / q3  ") == "weekly-sync-q3"
    assert recording_name("...") == "audio"


def test_audio_data_lines_come_from_recgo_config():
    s = _Settings()
    cfg = {"transcription": {"remote": {"endpoint": "https://llm.example/v1"}},
           "upload": {"enabled": True, "url": "https://rec.example/api/upload"}}
    assert audio_data_lines(s, cfg) == [("Recorded, not transcribed live", False),
                                        ("Nothing leaves this computer", False)]
    s.neverUpload = False
    s.audio_transcribe = True
    assert audio_data_lines(s, cfg) == [("Live transcription streams the audio to llm.example", True),
                                        ("The recording is uploaded to rec.example when it stops", True)]
    cfg["upload"] = {"enabled": True, "target": "user@host:/srv/recordings"}
    assert audio_data_lines(s, cfg)[1] == ("The recording is pushed to user@host:/srv/recordings when it stops", True)
    s.audio_transcribe = False
    assert audio_data_lines(s, {}) == [("Recorded, not transcribed live", False), ("Nothing leaves this computer", False)]


def test_audio_lines_from_recgo_drive_the_live_state(monkeypatch):
    rec = _recorder(monkeypatch, _Settings())
    rec.mode = RecordMode.audio
    rec.phase = Phase.recording
    rec._ingest("recording -> /home/u/walk-and-talk/2026.09.14-10.00-standup.mkv")
    rec._ingest("mic: alsa_input.usb")
    rec._ingest("monitor: alsa_output.usb.monitor")
    assert rec.where == "/home/u/walk-and-talk/2026.09.14-10.00-standup.mkv"
    assert rec.where_label == "2026.09.14-10.00-standup.mkv"
    assert (rec.mic_source, rec.monitor_source) == ("alsa_input.usb", "alsa_output.usb.monitor")
    rec._ingest("00:00:03  hearing: so the")
    assert rec.hearing == "so the"
    rec._ingest("00:00:05  narration: [Ana] so the plan is")
    rec._ingest("00:00:09  system: and the reply")
    assert rec.last_narration == "[Ana] so the plan is" and rec.hearing == ""
    assert [(e.kind, e.text, e.t) for e in rec.live_doc.events] == [
        (EventKind.narration, "[Ana] so the plan is", 5.0), (EventKind.sys, "system: and the reply", 9.0)]
    rec._ingest("monitor: none")
    assert rec.monitor_source == ""
    rec._ingest("error: live transcription pass failed: 502")
    assert rec.last_error.startswith("live transcription")
    assert not rec.system_audio_available
    rec.mark()
    assert rec.marks == 0


def test_finished_path_names_a_folder_or_the_recording_file():
    assert finished_path(["stopping...", "wrote /x/2026-09-14-desktop/SESSION.md"]) == "/x/2026-09-14-desktop"
    assert finished_path(["wrote /x/2026.09.14-10.00-standup.mkv", "Uploading /x/... -> https://r/api/upload ...",
                          "Uploaded to https://r/api/upload (ok)"]) == "/x/2026.09.14-10.00-standup.mkv"
    assert finished_path(["Error: pactl not found"]) is None
    assert uploaded_to(["wrote /x/a.mkv", "Uploaded to https://r/api/upload (ok)"]) == "https://r/api/upload (ok)"
    assert uploaded_to(["wrote /x/a.mkv"]) == ""


def test_build_arguments_screen_respects_mic_off(monkeypatch):
    s = _Settings()
    s.captureMic = False
    rec = _recorder(monkeypatch, s)
    args = rec.build_arguments(RecordMode.screen)
    assert "-no-audio" in args
    assert "-no-video" not in args


def test_build_arguments_window_mode_shoots_like_screen_mode(monkeypatch):
    s = _Settings()
    s.clickShots = False
    rec = _recorder(monkeypatch, s)
    args = rec.build_arguments(RecordMode.window)
    assert "-click-shots=false" in args and "-focus-shots=false" in args
    assert "-no-video" not in args and "-screen" not in args
    assert RecordMode.window.binary == "recgo-window"
    assert "-click-shots=false" not in rec.build_arguments(RecordMode.browser)


def _flag(args, name):
    return args[args.index(name) + 1]


def test_build_arguments_mixes_system_audio_only_when_audio_is_captured(monkeypatch):
    s = _Settings()
    s.systemAudio = True
    rec = _recorder(monkeypatch, s)
    assert _flag(rec.build_arguments(RecordMode.screen), "-system-audio") == "default"
    s.monitorDevice = "alsa_output.usb.monitor"
    assert _flag(rec.build_arguments(RecordMode.window), "-system-audio") == "alsa_output.usb.monitor"
    s.captureMic = False
    assert "-system-audio" not in rec.build_arguments(RecordMode.screen)


def test_build_arguments_pins_tab_by_target_id(monkeypatch):
    rec = _recorder(monkeypatch, _Settings())
    target = {"id": "1CB04B15", "title": "Acme", "url": "http://localhost:5173/"}
    args = rec.build_arguments(RecordMode.tab, target)
    assert args[args.index("-target") + 1] == "1CB04B15"
    assert "-target" not in rec.build_arguments(RecordMode.browser, target)


def test_stderr_lines_drive_tab_and_system_audio_state(monkeypatch):
    rec = _recorder(monkeypatch, _Settings())
    rec.phase = Phase.recording
    rec._ingest("attached to: Acme dashboard")
    rec._ingest("tab: 1CB04B15 http://localhost:5173/login")
    assert (rec.tab_title, rec.tab_id, rec.tab_url) == ("Acme dashboard", "1CB04B15", "http://localhost:5173/login")
    assert not rec.system_audio_available
    rec.system_audio_source = ""
    rec._ingest("audio: system audio mixed in from alsa_output.usb.monitor")
    assert rec.system_audio_available and rec.system_audio
    rec._ingest("system audio: off")
    assert rec.system_audio is False
    rec._ingest("system audio: on")
    assert rec.system_audio is True
    rec._ingest("system audio: no capture stream owned by pid 7")
    assert not rec.system_audio_available
    rec._ingest("capturing screen DP-1, 2560x1440")
    assert rec.source == "screen DP-1, 2560x1440"


def test_toggle_needs_a_running_mix(monkeypatch):
    rec = _recorder(monkeypatch, _Settings())
    assert rec.toggle_system_audio() is None
    rec.phase = Phase.recording
    rec.system_audio_source = "alsa_output.usb.monitor"
    rec.system_audio = True
    written = []

    class _Proc:
        def write(self, data):
            written.append(data)

    rec._proc = _Proc()
    assert rec.toggle_system_audio() is False
    assert rec.toggle_system_audio() is True
    assert written == [b"sysaudio off\n", b"sysaudio on\n"]


def test_recordable_tabs_filters_browser_internals():
    from recgo_app.cdp import recordable_tabs
    targets = [
        {"id": "a", "type": "page", "title": "Acme", "url": "http://localhost:5173/", "webSocketDebuggerUrl": "ws://x"},
        {"id": "b", "type": "page", "title": "New Tab", "url": "chrome://newtab/", "webSocketDebuggerUrl": "ws://y"},
        {"id": "c", "type": "service_worker", "title": "sw", "url": "http://localhost/sw.js", "webSocketDebuggerUrl": "ws://z"},
        {"id": "d", "type": "page", "title": "detached", "url": "http://example.com/"},
    ]
    assert [t["id"] for t in recordable_tabs(targets)] == ["a"]


def test_meet_acceptance_records_the_call_as_recgo_meet(monkeypatch):
    s = _Settings()
    rec = _recorder(monkeypatch, s)
    monkeypatch.setattr(rec, "_spawn", lambda mode: None)
    rec.start(RecordMode.audio, meeting=True)
    assert rec.name == "meet"
    assert rec.build_arguments(RecordMode.audio) == ["-headless", "-no-upload", "meet"]
    assert rec.facts[1] == "System audio: default output (own track)"
    assert s.systemAudio is False
    rec.phase = Phase.idle
    rec.start(RecordMode.audio)
    assert rec.name == "audio"
