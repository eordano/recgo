import os
import subprocess

PLUGIN = "recgo-hud"

SCRIPT = r"""
function isHud(w) { return !!(w && w.caption && w.caption.indexOf("Recgo HUD") === 0); }
function isLive(w) { return !!(w && w.caption && w.caption.indexOf("Recgo Live") === 0); }
function place(w) {
    var a = workspace.clientArea(KWin.PlacementArea, w);
    var g = w.frameGeometry;
    if (isHud(w)) {
        var hx = %(hud_x)d, hy = %(hud_y)d;
        if (hx < 0 || hy < 0 || hx + 40 > a.x + a.width || hy + 40 > a.y + a.height) {
            hx = a.x + a.width - g.width - 36;
            hy = a.y + a.height - g.height - 82;
        }
        w.frameGeometry = { x: hx, y: hy, width: g.width, height: g.height };
    } else if (isLive(w)) {
        w.frameGeometry = { x: a.x + 40, y: a.y + 40, width: g.width, height: g.height };
    }
}
function pin(w) {
    if (!isHud(w) && !isLive(w)) return;
    w.keepAbove = true;
    w.onAllDesktops = true;
    w.skipTaskbar = true;
    w.skipPager = true;
    w.skipSwitcher = true;
    place(w);
    if (isLive(w)) {
        workspace.windowList().forEach(function (o) { if (isHud(o)) workspace.raiseWindow(o); });
    }
}
var previous = null;
workspace.windowAdded.connect(pin);
workspace.windowList().forEach(pin);
function raiseHud() {
    workspace.windowList().forEach(function (o) { if (isHud(o)) workspace.raiseWindow(o); });
}
function onCurrentDesktop(w) {
    if (w.onAllDesktops) return true;
    var cur = workspace.currentDesktop;
    return w.desktops.some(function (d) { return d.id === cur.id; });
}
function topWindowHere() {
    var s = workspace.stackingOrder;
    for (var i = s.length - 1; i >= 0; i--) {
        var o = s[i];
        if (isHud(o) || !o.normalWindow || o.minimized || !onCurrentDesktop(o)) continue;
        return o;
    }
    return null;
}
workspace.windowActivated.connect(function (w) {
    if (!w) return;
    if (isHud(w)) {
        var back = previous && previous !== w && onCurrentDesktop(previous) ? previous : topWindowHere();
        if (back) workspace.activeWindow = back;
        return;
    }
    previous = w;
    raiseHud();
});

"""


def _call(method, sig=None, *args):
    cmd = ["busctl", "--user", "--timeout=3", "call", "org.kde.KWin", "/Scripting",
           "org.kde.kwin.Scripting", method]
    if sig:
        cmd += [sig] + list(args)
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=6)
    except (OSError, subprocess.TimeoutExpired):
        return None
    return r.stdout.strip() if r.returncode == 0 else None


def script_path():
    base = os.environ.get("XDG_RUNTIME_DIR") or "/tmp"
    return os.path.join(base, "recgo-hud.kwin.js")


def load(hud_pos=None):
    if _call("isScriptLoaded", "s", PLUGIN) == "b true":
        _call("unloadScript", "s", PLUGIN)
    x, y = hud_pos if hud_pos else (-1, -1)
    path = script_path()
    try:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(SCRIPT % {"hud_x": x, "hud_y": y})
    except OSError:
        return False
    r = _call("loadScript", "ss", path, PLUGIN)
    if r is None or r.endswith(" -1"):
        return False
    _call("start")
    return True


def unload():
    _call("unloadScript", "s", PLUGIN)
