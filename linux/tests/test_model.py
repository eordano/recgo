import os
import struct
import wave

from recgo_app.model import EventKind, parse_session_doc, scan_library, waveform_bins

SAMPLE = """# Session: login popup dismisses on its own

Start: 2026-08-27 23:58:22
Folder: /home/user/src/acme
Page: https://dcl.one/play/ — Decentraland (BEVY)
Capture: xdg-portal-screencast + kwin-screenshot2
Host: workstation · NixOS
Displays: 2560x1440
Recorded 82s by recgo-tab · 12 clicks · 19 errors · 15 utterances

Initial screenshot: 0001.png
00.00.01  Click: 787,199 on #ui3-overlay > div text: Sign in → 0002.png
00.00.01  **user narration**: Okay, the avatar
00.00.05  Click: 10,20 → 0003.png — screen did not repaint
00.00.07  Mark: 1 → 0004.png
00.00.08  Focus: Konsole
00.00.09  Window: Save dialog → 0005.png
00.00.10  Error: network GET /api 500
00.00.11  Error: TypeError: x is undefined
00.00.12  console.error: boom
00.00.13  console.log: hello
00.00.14  Navigate: https://example.com
00.01.02  HMR: rebuilt
"""


def test_parse_header_and_events():
    doc = parse_session_doc(SAMPLE)
    assert doc.title == "login popup dismisses on its own"
    assert doc.start == "2026-08-27 23:58:22"
    assert doc.capture.startswith("xdg-portal")
    assert doc.host == "workstation · NixOS"
    assert doc.duration_sec == 82
    assert doc.tool == "recgo-tab"
    assert doc.initial_shot == "0001.png"
    kinds = [e.kind for e in doc.events]
    assert kinds == [EventKind.shot, EventKind.click, EventKind.narration, EventKind.click, EventKind.mark,
                     EventKind.focus, EventKind.window, EventKind.network, EventKind.error, EventKind.error,
                     EventKind.console, EventKind.sys, EventKind.sys]
    click = doc.events[1]
    assert (click.x, click.y, click.img) == (787, 199, "0002.png")
    assert click.text.startswith("787,199 on ")
    assert doc.events[3].text.endswith("screen did not repaint")
    assert doc.events[4].text == "mark 1"
    assert doc.events[6].img == "0005.png"
    assert doc.events[-1].t == 62 and doc.events[-1].text == "HMR: rebuilt"
    assert len(doc.shots) == 5 and doc.marks == 1 and doc.errors == 2


def test_scan_library_skips_provisional(tmp_path):
    done = tmp_path / "2026-09-04-10-00-hello"
    done.mkdir()
    (done / "SESSION.md").write_text(SAMPLE, encoding="utf-8")
    (tmp_path / "2026-09-04-10-05-recording-4242").mkdir()
    (tmp_path / "stray.txt").write_text("x")
    sessions = scan_library(str(tmp_path))
    assert [s.id for s in sessions] == ["2026-09-04-10-00-hello"]
    assert sessions[0].meta == "23:58 · 01:22 · tab · 5 shots · 2 errors · 1 mark"


def test_waveform_bins(tmp_path):
    path = os.path.join(tmp_path, "audio.wav")
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(16000)
        frames = b"".join(struct.pack("<h", 0) for _ in range(8000))
        frames += b"".join(struct.pack("<h", 20000 if i % 2 else -20000) for i in range(8000))
        w.writeframes(frames)
    bins = waveform_bins(path, bins=4)
    assert len(bins) == 4
    assert bins[0] == 0 and bins[1] == 0
    assert bins[2] == 1.0 and bins[3] == 1.0
