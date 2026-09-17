import getpass
import glob
import os
import shutil
import sys
import tomllib
from enum import Enum
from urllib.parse import urlsplit

from PySide6.QtCore import QObject, QSettings, Signal

from recgo_app import APP_ID


class RecordMode(str, Enum):
    screen = "screen"
    window = "window"
    browser = "browser"
    tab = "tab"
    audio = "audio"

    @property
    def label(self):
        return {"screen": "Screen", "window": "Window", "browser": "Browser", "tab": "This Tab",
                "audio": "Audio only"}[self.value]

    @property
    def menu_text(self):
        """The tray row: an ellipsis where a picker follows the click."""
        return self.label + "…" if self.picks_at_start else self.label

    @property
    def shortcut_key(self):
        return {"screen": "shortcutRecordScreen", "window": "shortcutRecordWindow",
                "browser": "shortcutRecordBrowser", "tab": "shortcutRecordTab",
                "audio": "shortcutRecordAudio"}[self.value]

    @property
    def binary(self):
        """Audio only is `recgo <name>` itself, run -headless: the same
        three-track file, devices, watchdog and upload as the terminal."""
        return {"screen": "recgo-desktop", "window": "recgo-window", "audio": "recgo",
                "browser": "recgo-browser", "tab": "recgo-tab"}[self.value]

    @property
    def needs_cdp(self):
        return self in (RecordMode.browser, RecordMode.tab)

    @property
    def captures_screen(self):
        return self in (RecordMode.screen, RecordMode.window)

    @property
    def picks_at_start(self):
        return self in (RecordMode.tab, RecordMode.window, RecordMode.audio)

    @property
    def packing_label(self):
        """What happens between Stop and idle, for the tray and HUD."""
        if self == RecordMode.audio:
            return "Finishing the recording"
        return "Packing the %s session" % self.label.lower()


def documents_dir():
    d = os.environ.get("XDG_DOCUMENTS_DIR")
    if d:
        return d
    return os.path.join(os.path.expanduser("~"), "Documents")


def configured_out_root():
    path = os.path.join(os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config")), "recgo", "config.toml")
    try:
        import tomllib
        with open(path, "rb") as f:
            value = tomllib.load(f).get("recording", {}).get("output_dir", "")
    except (OSError, ValueError):
        return ""
    return os.path.expanduser(value) if value else ""


def default_out_root():
    return configured_out_root() or os.path.join(os.path.expanduser("~"), "walk-and-talk")


SHORTCUT_KEYS = ["shortcutRecordScreen", "shortcutRecordWindow", "shortcutRecordBrowser",
                 "shortcutRecordTab", "shortcutRecordAudio", "shortcutMark", "shortcutToggleHUD",
                 "shortcutStop", "shortcutOpenLibrary"]

DEFAULTS = {
    "outRoot": default_out_root(),
    "menuTimer": True,
    "meetPrompt": False,
    "autoTitle": True,
    "defaultMode": "screen",
    "clickShots": True,
    "focusShots": True,
    "captureMic": True,
    "micDevice": "",
    "systemAudio": False,
    "monitorDevice": "",
    "sttBackend": "auto",
    "sttLanguage": "",
    "whisperModel": "",
    "whisperBin": "",
    "liveNarrationHUD": True,
    "liveOnStart": False,
    "neverUpload": True,
    "syncEnabled": False,
    "syncTarget": "",
    "syncKey": "",
    "syncURL": "",
    "portalEnabled": False,
    "portalURL": "",
    "portalRoom": "",
    "audioName": "",
    "binDir": "",
    "extraRoot": "",
    **{k: "" for k in SHORTCUT_KEYS},
}

MANAGED_CONF = os.path.join(
    os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config")), "recgo", "managed.conf")

RECORDER_CONF = os.path.join(
    os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config")), "recgo", "config.toml")

MODELS_DIR = os.path.expanduser("~/.local/share/recgo/models")


def recorder_config(path=RECORDER_CONF):
    """The recorders' own config.toml: the remote endpoint and the upload
    target the CLIs fall back to when the app passes no override."""
    try:
        with open(path, "rb") as fh:
            return tomllib.load(fh)
    except (OSError, tomllib.TOMLDecodeError):
        return {}


def whisper_models(root=MODELS_DIR):
    return sorted(p for p in glob.glob(os.path.join(root, "ggml-*.bin")) if "silero" not in p)


def host_of(url):
    u = urlsplit(url)
    return u.netloc or url


def data_lines(s, config=None, models=None):
    """Where this session's bytes go, as (text, leaves_machine) pairs, from
    the same values the recorder flags are built from. The first line is
    always the transcription; the rest appear only when something is on."""
    config = recorder_config() if config is None else config
    models = whisper_models() if models is None else models
    endpoint = ((config.get("transcription") or {}).get("remote") or {}).get("endpoint", "")
    where = host_of(endpoint) if endpoint else "the endpoint in config.toml"
    backend = s.effective_stt_backend
    model = os.path.basename(s.whisperModel) if s.whisperModel else (os.path.basename(models[-1]) if models else "")
    if backend == "auto":
        backend = "local" if model else "remote"
    lines = []
    if not s.captureMic:
        lines.append(("No microphone: nothing is transcribed", False))
    elif backend == "none":
        lines.append(("Narration is recorded but not transcribed", False))
    elif backend == "local":
        lines.append(("Narration is transcribed on this computer (%s)" % (model or "whisper.cpp"), False))
    elif backend == "realtime":
        lines.append(("Narration audio is streamed to %s as you speak" % where, True))
    else:
        lines.append(("Narration audio is uploaded to %s" % where, True))
    if s.captureMic and backend in ("remote", "realtime") and s.autoTitle:
        lines.append(("The transcript is uploaded to %s for the title" % where, True))
    if s.portal_active:
        lines.append(("Portal room open at %s: the room reads the whole session folder" % host_of(s.portalURL), True))
    if s.sync_active:
        lines.append(("Finished sessions are pushed to %s" % s.syncTarget, True))
    if not any(leaves for _, leaves in lines):
        lines.append(("Nothing leaves this computer", False))
    return lines


def audio_data_lines(s, config=None):
    """Where an Audio only recording's bytes go. It runs `recgo <name>`, so
    the answers come from recgo's own config.toml, not the session flags."""
    config = recorder_config() if config is None else config
    endpoint = ((config.get("transcription") or {}).get("remote") or {}).get("endpoint", "")
    where = host_of(endpoint) if endpoint else "the endpoint in config.toml"
    lines = []
    if s.audio_transcribe:
        lines.append(("Live transcription streams the audio to %s" % where, True))
    else:
        lines.append(("Recorded, not transcribed live", False))
    up = config.get("upload") or {}
    if up.get("enabled") and not s.neverUpload:
        if up.get("url"):
            lines.append(("The recording is uploaded to %s when it stops" % host_of(up["url"]), True))
        elif up.get("target"):
            lines.append(("The recording is pushed to %s when it stops" % up["target"], True))
    if not any(leaves for _, leaves in lines):
        lines.append(("Nothing leaves this computer", False))
    return lines


def audio_output_dir(config=None):
    config = recorder_config() if config is None else config
    d = (config.get("recording") or {}).get("output_dir", "")
    return os.path.expanduser(d) if d else os.path.expanduser("~/archive/recordings")


def data_summary(lines):
    """One quiet line for the HUD and tray: the first thing that leaves the
    machine, or the local promise."""
    leaving = [t for t, leaves in lines if leaves]
    if not leaving:
        return "Nothing leaves this computer", False
    extra = len(leaving) - 1
    return (leaving[0] + (" · +%d more" % extra if extra else ""), True)

AUTOSTART_DIR = os.path.join(
    os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config")), "autostart")
AUTOSTART_FILE = os.path.join(AUTOSTART_DIR, APP_ID + ".desktop")


def read_managed():
    """The module-owned INI, parsed by hand: QSettings would open it
    read-write and can rewrite it on teardown, which is how keys went missing."""
    import configparser
    out = {}
    if not os.path.exists(MANAGED_CONF):
        return out
    cp = configparser.ConfigParser(interpolation=None)
    cp.optionxform = str
    try:
        cp.read(MANAGED_CONF, encoding="utf-8")
    except configparser.Error:
        return out
    for section in cp.sections():
        for key, value in cp.items(section):
            if key in DEFAULTS:
                out[key] = value.strip()
    return out


class AppSettings(QObject):
    changed = Signal(str)
    _instance = None

    @classmethod
    def shared(cls):
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        super().__init__()
        QSettings.setDefaultFormat(QSettings.Format.IniFormat)
        self._q = QSettings("recgo", "Recgo")
        self._managed = read_managed()

    def managed(self, key):
        return key in self._managed

    def shortcut_managed(self, key):
        return self.managed(key)

    def get(self, key):
        default = DEFAULTS[key]
        v = self._managed[key] if key in self._managed else self._q.value(key, default)
        if isinstance(default, bool):
            if isinstance(v, str):
                return v.lower() in ("true", "1", "yes")
            return bool(v)
        return "" if v is None else str(v)

    def set(self, key, value):
        if self.get(key) == value:
            return
        self._q.setValue(key, value)
        self._q.sync()
        self.changed.emit(key)

    def __getattr__(self, name):
        if name in DEFAULTS:
            return self.get(name)
        raise AttributeError(name)

    def __setattr__(self, name, value):
        if name in DEFAULTS:
            self.set(name, value)
        else:
            super().__setattr__(name, value)

    @property
    def default_mode(self):
        try:
            return RecordMode(self.get("defaultMode"))
        except ValueError:
            return RecordMode.screen

    @property
    def out_root(self):
        return os.path.expanduser(self.get("outRoot") or default_out_root())

    @property
    def portal_active(self):
        return self.portalEnabled and not self.neverUpload and bool(self.portalURL)

    @property
    def sync_active(self):
        return self.syncEnabled and not self.neverUpload and bool(self.syncTarget)

    def session_url(self, session_id, name=None):
        base = self.get("syncURL").rstrip("/")
        if not base:
            return None
        url = "%s/%s" % (base, session_id)
        return url + "/" + name if name else url + "/"

    def portal_page(self, room):
        """The human-facing room page derived from the wss endpoint:
        wss://host/ws -> https://host/?room=<room>."""
        from urllib.parse import urlsplit, urlunsplit, urlencode
        u = urlsplit(self.get("portalURL"))
        if not u.netloc:
            return None
        scheme = "https" if u.scheme in ("wss", "https") else "http"
        return urlunsplit((scheme, u.netloc, "/", urlencode({"room": room}) if room else "", ""))

    def remote_location(self, session_id):
        """Where the synced copy of a session lives, for handing to an agent
        or a teammate on another host. The fleet mounts the sync destination
        at the same absolute path on every host, so when that directory is
        mounted here the plain path is returned -- it pastes unchanged
        anywhere -- and the user@host: form only when it is not."""
        if not self.sync_active:
            return None
        t = self.syncTarget
        remote = t + session_id if t.endswith("/") else t + "/" + session_id
        _, _, path = remote.partition(":")
        if path.startswith("/") and os.path.isdir(os.path.dirname(path)):
            return path
        return remote

    @property
    def monitor_label(self):
        return self.monitorDevice or "default output"

    @property
    def mic_label(self):
        return self.micDevice or "system default microphone"

    def data_lines(self):
        return data_lines(self)

    def audio_data_lines(self):
        return audio_data_lines(self)

    @property
    def audio_transcribe(self):
        """recgo's live transcription is the t key: audio to the configured
        endpoint, so it stays off under Never upload and with no backend."""
        return not self.neverUpload and self.sttBackend != "none"

    @property
    def effective_stt_backend(self):
        b = self.sttBackend
        if self.neverUpload and b in ("auto", "remote", "realtime"):
            return "local"
        return b

    @staticmethod
    def launcher_path():
        exe = os.environ.get("RECGO_APP_EXE") or shutil.which("recgo-app")
        if exe:
            return os.path.realpath(exe)
        return "%s -m recgo_app" % sys.executable

    @property
    def autostart_managed(self):
        return os.path.islink(AUTOSTART_FILE) and os.readlink(AUTOSTART_FILE).startswith("/nix/store")

    @property
    def launch_at_login(self):
        return os.path.exists(AUTOSTART_FILE)

    @launch_at_login.setter
    def launch_at_login(self, on):
        if self.autostart_managed:
            return
        if on:
            os.makedirs(AUTOSTART_DIR, exist_ok=True)
            with open(AUTOSTART_FILE, "w", encoding="utf-8") as fh:
                fh.write("[Desktop Entry]\nType=Application\nName=Recgo\nExec=%s\nIcon=%s\n"
                         "X-KDE-autostart-after=panel\nOnlyShowIn=KDE;\n"
                         % (self.launcher_path(), APP_ID))
        else:
            try:
                os.remove(AUTOSTART_FILE)
            except OSError:
                pass

    def geometry(self, name):
        v = self._q.value("geometry/" + name)
        return v if v else None

    def save_geometry(self, name, data):
        self._q.setValue("geometry/" + name, data)
        self._q.sync()

    def resolve_binary(self, name):
        dirs = []
        if self.binDir:
            dirs.append(os.path.expanduser(self.binDir))
        helpers = os.environ.get("RECGO_HELPERS")
        if helpers:
            dirs.append(helpers)
        dirs += os.environ.get("PATH", "").split(":")
        dirs += extra_path_dirs()
        for d in dirs:
            if not d:
                continue
            p = os.path.join(d, name)
            if os.path.isfile(p) and os.access(p, os.X_OK):
                return p
        return None


def extra_path_dirs():
    home = os.path.expanduser("~")
    return [os.path.join(home, ".local/bin"), os.path.join(home, ".nix-profile/bin"),
            "/etc/profiles/per-user/%s/bin" % getpass.getuser(),
            "/run/current-system/sw/bin", "/usr/local/bin", "/usr/bin", "/bin"]
