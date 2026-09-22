#!/usr/bin/env python3
"""Opt-in end-to-end test on an already booted, consented emulator. No physical phones."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import xml.etree.ElementTree as ET

p = argparse.ArgumentParser()
p.add_argument("--serial", required=True)
p.add_argument("--adb", default="adb")
p.add_argument("--scrcpy", default="scrcpy")
p.add_argument("--recorder", default=str(Path(__file__).resolve().parents[1] / "recgo-android"))
args = p.parse_args()
if not args.serial.startswith("emulator-"):
    p.error("this test only accepts emulator-* serials")
package = "dev.eordano.recgo.android"

def adb(*command):
    return subprocess.check_output([args.adb, "-s", args.serial, *command], text=True, timeout=20)

adb("shell", "am", "start", "-W", "-f", "0x14000000", "-n", package + "/.MainActivity")
adb("shell", "uiautomator", "dump", "/sdcard/recgo-test.xml")
tree = ET.fromstring(adb("shell", "cat", "/sdcard/recgo-test.xml"))
targets = {}
for node in tree.iter("node"):
    rid = node.get("resource-id", "")
    if rid.startswith(package + ":id/demo_"):
        import re
        x1, y1, x2, y2 = map(int, re.findall(r"\d+", node.get("bounds")))
        targets[rid.split("/")[-1]] = ((x1 + x2) // 2, (y1 + y2) // 2)
assert {"demo_button", "demo_toggle", "demo_password"} <= targets.keys(), targets

root = Path(tempfile.mkdtemp(prefix="recgo-android-e2e-"))

def wait_for_click(target):
    # Keep the source window alive until Android has delivered its semantic
    # event. Leaving immediately can legitimately make getSource() return null.
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        for journal in root.glob("*/events.jsonl"):
            for line in journal.read_text().splitlines():
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue  # The append may still be in flight.
                if event.get("node", {}).get("resourceId") == package + ":id/" + target:
                    return
        time.sleep(0.05)
    raise AssertionError("no captured click on " + target)

command = [args.recorder, "--serial", args.serial, "--adb", args.adb,
           "--package", package, "--screenshots", "--video", "--logs",
           "--scrcpy", args.scrcpy, "--out", str(root), "--duration", "12s"]
with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as proc:
    try:
        while True:
            line = proc.stderr.readline()
            print(line, end="", flush=True)
            if line.startswith("Recording locally:"):
                break
            if not line:
                raise AssertionError("recorder exited before ready")
        state = adb("shell", "dumpsys", "accessibility")
        assert "TYPE_VIEW_CLICKED" in state, "click event subscription was not active"
        print("Demo targets:", targets, flush=True)
        for target in ("demo_button", "demo_toggle"):
            adb("shell", "input", "tap", *map(str, targets[target]))
            wait_for_click(target)
        adb("shell", "input", "tap", *map(str, targets["demo_password"]))
        adb("shell", "input", "text", "recgo_synthetic_secret_731")
        adb("shell", "input", "keyevent", "KEYCODE_BACK")
        adb("shell", "input", "tap", *map(str, targets["demo_password"]))
        wait_for_click("demo_password")
        adb("shell", "input", "keyevent", "KEYCODE_HOME")
        stdout, stderr = proc.communicate(timeout=30)
        print(stdout, stderr)
        assert proc.returncode == 0, proc.returncode
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait()

session_dir, = root.iterdir()
raw = (session_dir / "session.json").read_text()
session = json.loads(raw)
events = session["events"]
clicks = [event for event in events if event["kind"] == "click"]
ids = {event.get("node", {}).get("resourceId") for event in clicks}
assert package + ":id/demo_button" in ids, events
assert package + ":id/demo_toggle" in ids, events
for event in clicks:
    node = event.get("node", {})
    target = node.get("resourceId", "").split("/")[-1]
    if target in targets:
        x, y = targets[target]
        left, top, right, bottom = node["bounds"]
        assert left <= x < right and top <= y < bottom, node
toggle = next(event["node"] for event in clicks if event.get("node", {}).get("resourceId") == package + ":id/demo_toggle")
assert toggle["checked"] is True, toggle
password = [event for event in clicks if event.get("node", {}).get("resourceId") == package + ":id/demo_password"]
assert password and password[0]["node"]["redacted"], events
assert "recgo_synthetic_secret_731" not in raw
assert "recgo_synthetic_secret_731" not in (session_dir / "events.jsonl").read_text()
assert all(event.get("package", package) == package for event in events)
for event in clicks:
    shot = event["screenshot"]
    assert shot.get("file"), shot
    assert shot["endMs"] >= shot["startMs"] >= event["t"], event
    assert (session_dir / shot["file"]).read_bytes().startswith(b"\x89PNG\r\n\x1a\n")
assert (session_dir / "video.mkv").stat().st_size > 1000
assert "localabstract:recgo_android" not in adb("forward", "--list")
assert session_dir.stat().st_mode & 0o777 == 0o700
print("PASS: IDs, bounds, password redaction, package filtering, delayed screenshots, video, forward cleanup")
print(session_dir)
