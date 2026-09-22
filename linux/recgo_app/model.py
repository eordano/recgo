import os
import re
import wave
from array import array
from dataclasses import dataclass, field
from enum import Enum
from itertools import count

from recgo_app.theme import Theme


class EventKind(str, Enum):
    narration = "narration"
    click = "click"
    mark = "mark"
    focus = "focus"
    window = "window"
    shot = "shot"
    error = "error"
    network = "network"
    console = "console"
    sys = "sys"

    @property
    def color(self):
        return {
            EventKind.narration: Theme.accent,
            EventKind.click: Theme.click_blue,
            EventKind.mark: Theme.amber,
            EventKind.focus: Theme.focus_purple,
            EventKind.window: Theme.focus_purple,
            EventKind.shot: Theme.shot_green,
            EventKind.error: Theme.pink,
            EventKind.network: Theme.net_orange,
            EventKind.console: Theme.muted,
            EventKind.sys: Theme.faint,
        }[self]

    @property
    def text_color(self):
        return {
            EventKind.narration: Theme.text,
            EventKind.click: Theme.secondary,
            EventKind.focus: Theme.secondary,
            EventKind.window: Theme.secondary,
            EventKind.mark: Theme.amber,
            EventKind.error: Theme.pink,
            EventKind.network: Theme.net_text,
            EventKind.shot: Theme.muted,
            EventKind.console: Theme.muted,
            EventKind.sys: Theme.muted,
        }[self]

    @property
    def mono(self):
        return self not in (EventKind.narration, EventKind.mark)


_ids = count(1)


@dataclass
class SessionEvent:
    t: float
    kind: EventKind
    text: str
    img: str | None = None
    x: int | None = None
    y: int | None = None
    id: int = field(default_factory=lambda: next(_ids))

    @property
    def stamp(self):
        return clock_string(self.t)


@dataclass
class SessionDoc:
    title: str = ""
    start: str = ""
    folder: str = ""
    page: str = ""
    capture: str = ""
    host: str = ""
    displays: str = ""
    tool: str = ""
    duration_sec: int = 0
    initial_shot: str | None = None
    notes: list = field(default_factory=list)
    events: list = field(default_factory=list)

    @property
    def shots(self):
        return [e for e in self.events if e.img]

    @property
    def marks(self):
        return sum(1 for e in self.events if e.kind == EventKind.mark)

    @property
    def errors(self):
        return sum(1 for e in self.events if e.kind == EventKind.error)


def clock_string(sec):
    s = max(0, int(round(sec)))
    return "%02d:%02d" % (s // 60, s % 60)


CLICK_RE = re.compile(r"^(\d\d\.\d\d\.\d\d)  Click: (.*?) → (\d{4}\.png)( — screen did not repaint)?\s*$")
SHOT_RE = re.compile(r"^(\d\d\.\d\d\.\d\d)  (Mark|Focus|Window): (.*?)(?: → (\d{4}\.png))?\s*$")
NARR_RE = re.compile(r"^(\d\d\.\d\d\.\d\d)  \*\*user narration\*\*: (.*)$")
OTHER_RE = re.compile(r"^(\d\d\.\d\d\.\d\d)  ([A-Za-z._]+): (.*)$")
COORD_RE = re.compile(r"^(\d+),(\d+)(?: on (.*))?$", re.S)
DURATION_RE = re.compile(r"Recorded (\d+)s")


def clock_seconds(s):
    p = s.split(".")
    if len(p) != 3:
        return 0.0
    try:
        return int(p[0]) * 3600 + int(p[1]) * 60 + int(p[2])
    except ValueError:
        return 0.0


def parse_session_doc(text):
    doc = SessionDoc()
    for line in text.split("\n"):
        if line.startswith("# Session: "):
            doc.title = line[len("# Session: "):]
            continue
        for prefix, attr in (("Start: ", "start"), ("Folder: ", "folder"), ("Page: ", "page"),
                             ("Capture: ", "capture"), ("Host: ", "host"),
                             ("Displays: ", "displays")):
            if line.startswith(prefix):
                setattr(doc, attr, line[len(prefix):])
                break
        else:
            if line.startswith("Initial screenshot: "):
                doc.initial_shot = line[len("Initial screenshot: "):]
                doc.events.append(SessionEvent(0, EventKind.shot, "initial screenshot",
                                               img=doc.initial_shot))
                continue
            if line.startswith("Recorded "):
                m = DURATION_RE.search(line)
                if m:
                    doc.duration_sec = int(m.group(1))
                if "by recgo" in line:
                    doc.tool = line[line.index("by recgo") + 3:].split(" ")[0]
                doc.notes.append(line)
                continue
            m = CLICK_RE.match(line)
            if m:
                x = y = None
                what = m.group(2)
                c = COORD_RE.match(what)
                if c:
                    x, y = int(c.group(1)), int(c.group(2))
                    what = "%s,%s" % (c.group(1), c.group(2))
                    if c.group(3):
                        what += " on " + c.group(3)
                if m.group(4):
                    what += " — screen did not repaint"
                doc.events.append(SessionEvent(clock_seconds(m.group(1)), EventKind.click, what,
                                               img=m.group(3), x=x, y=y))
                continue
            m = SHOT_RE.match(line)
            if m:
                kind = {"Mark": EventKind.mark, "Focus": EventKind.focus}.get(
                    m.group(2), EventKind.window)
                text_ = ("mark " + m.group(3)) if kind == EventKind.mark else m.group(3)
                doc.events.append(SessionEvent(clock_seconds(m.group(1)), kind, text_,
                                               img=m.group(4) or None))
                continue
            m = NARR_RE.match(line)
            if m:
                doc.events.append(SessionEvent(clock_seconds(m.group(1)), EventKind.narration,
                                               m.group(2)))
                continue
            m = OTHER_RE.match(line)
            if m:
                label, body = m.group(2), m.group(3)
                if label == "Error" and body.startswith("network"):
                    kind, text_ = EventKind.network, body
                elif label == "Error":
                    kind, text_ = EventKind.error, body
                elif label == "Click":
                    # Android metadata-only sessions deliberately have no image.
                    kind, text_ = EventKind.click, body
                elif label.startswith("console."):
                    kind = EventKind.error if label.endswith("error") else EventKind.console
                    text_ = "%s: %s" % (label, body)
                else:
                    kind, text_ = EventKind.sys, "%s: %s" % (label, body)
                doc.events.append(SessionEvent(clock_seconds(m.group(1)), kind, text_))
            continue
    return doc


@dataclass
class LibrarySession:
    id: str
    path: str
    doc: SessionDoc
    modified: float

    @property
    def duration_label(self):
        return clock_string(self.doc.duration_sec)

    @property
    def meta(self):
        parts = []
        if len(self.doc.start) >= 16:
            parts.append(self.doc.start[11:16])
        parts.append(self.duration_label)
        if self.doc.tool:
            parts.append(self.doc.tool.replace("recgo-", ""))
        parts.append("%d shots" % len(self.doc.shots))
        if self.doc.errors:
            parts.append("%d error%s" % (self.doc.errors, "" if self.doc.errors == 1 else "s"))
        if self.doc.marks:
            parts.append("%d mark%s" % (self.doc.marks, "" if self.doc.marks == 1 else "s"))
        return " · ".join(parts)

    def file(self, name):
        return os.path.join(self.path, name)


_docs = {}


def scan_library(root):
    out = []
    try:
        entries = list(os.scandir(root))
    except OSError:
        return out
    for e in entries:
        if not e.is_dir() or "-recording-" in e.name:
            continue
        md = os.path.join(e.path, "SESSION.md")
        try:
            st = os.stat(md)
        except OSError:
            continue
        key = (md, st.st_mtime_ns, st.st_size)
        doc = _docs.get(key)
        if doc is None:
            try:
                with open(md, encoding="utf-8") as fh:
                    text = fh.read()
            except OSError:
                continue
            if len(_docs) > 2000:
                _docs.clear()
            doc = _docs[key] = parse_session_doc(text)
        out.append(LibrarySession(e.name, e.path, doc, st.st_mtime))
    out.sort(key=lambda s: s.modified, reverse=True)
    return out


def waveform_bins(path, bins=46):
    try:
        with wave.open(path, "rb") as w:
            channels = w.getnchannels()
            width = w.getsampwidth()
            frames = w.getnframes()
            if frames <= 0 or width != 2:
                return []
            raw = w.readframes(frames)
    except (OSError, wave.Error, EOFError):
        return []
    samples = array("h")
    samples.frombytes(raw[: len(raw) - len(raw) % 2])
    n = len(samples) // channels
    if n == 0:
        return []
    out = [0.0] * bins
    per = max(1, n // bins)
    for b in range(bins):
        lo = b * per
        hi = min(n, lo + per)
        if lo >= hi:
            break
        stride = max(1, (hi - lo) // 500)
        acc = 0.0
        cnt = 0
        for i in range(lo, hi, stride):
            v = samples[i * channels] / 32768.0
            acc += v * v
            cnt += 1
        out[b] = (acc / cnt) ** 0.5 if cnt else 0.0
    peak = max(out) if out else 0
    if peak > 0:
        out = [v / peak for v in out]
    return out
