from recgo_app.settings import AppSettings, data_lines, data_summary, host_of


class _Settings:
    captureMic = True
    autoTitle = True
    whisperModel = ""
    effective_stt_backend = "auto"
    portal_active = False
    portalURL = ""
    sync_active = False
    syncTarget = ""


CONFIG = {"transcription": {"remote": {"endpoint": "https://llm.example.dev/v1"}}}


def test_auto_with_a_local_model_stays_local():
    lines = data_lines(_Settings(), CONFIG, ["/m/ggml-base.bin"])
    assert lines == [("Narration is transcribed on this computer (ggml-base.bin)", False),
                     ("Nothing leaves this computer", False)]
    assert data_summary(lines) == ("Nothing leaves this computer", False)


def test_auto_without_a_model_names_the_endpoint_host():
    lines = data_lines(_Settings(), CONFIG, [])
    assert lines[0] == ("Narration audio is uploaded to llm.example.dev", True)
    assert lines[1] == ("The transcript is uploaded to llm.example.dev for the title", True)
    text, leaves = data_summary(lines)
    assert leaves and text == "Narration audio is uploaded to llm.example.dev · +1 more"


def test_realtime_portal_and_sync_each_get_a_line():
    s = _Settings()
    s.effective_stt_backend = "realtime"
    s.autoTitle = False
    s.portal_active = True
    s.portalURL = "wss://portal.example/ws"
    s.sync_active = True
    s.syncTarget = "host:/srv/sessions"
    texts = [t for t, leaves in data_lines(s, CONFIG, []) if leaves]
    assert texts == ["Narration audio is streamed to llm.example.dev as you speak",
                     "Portal room open at portal.example: the room reads the whole session folder",
                     "Finished sessions are pushed to host:/srv/sessions"]


def test_no_mic_and_no_backend_are_said_plainly():
    s = _Settings()
    s.captureMic = False
    assert data_lines(s, {}, [])[0] == ("No microphone: nothing is transcribed", False)
    s.captureMic = True
    s.effective_stt_backend = "none"
    assert data_lines(s, {}, [])[0] == ("Narration is recorded but not transcribed", False)
    s.effective_stt_backend = "remote"
    assert data_lines(s, {}, [])[0][0].endswith("the endpoint in config.toml")
    assert host_of("https://llm.example.dev/v1") == "llm.example.dev"

def test_remote_location_is_the_plain_path_when_the_destination_is_mounted_here(tmp_path):
    s = _Settings()
    s.sync_active = True
    s.syncTarget = "user@host:" + str(tmp_path)
    assert AppSettings.remote_location(s, "2026-01-01-00-00-x") == str(tmp_path / "2026-01-01-00-00-x")
    s.syncTarget = "user@host:" + str(tmp_path) + "/"
    assert AppSettings.remote_location(s, "x") == str(tmp_path / "x")
    s.syncTarget = "user@host:/nowhere/walk-and-talk"
    assert AppSettings.remote_location(s, "x") == "user@host:/nowhere/walk-and-talk/x"
    s.sync_active = False
    assert AppSettings.remote_location(s, "x") is None
