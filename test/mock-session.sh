#!/usr/bin/env bash
set -uo pipefail

FLAVOUR="${1:?usage: mock-session.sh <sway|kde> -- <command...>}"
shift
[ "${1:-}" = "--" ] && shift

need() {
  local p
  p=$(nix-build '<nixpkgs>' --no-out-link -A "$1" 2>/dev/null | tail -1)
  [ -n "$p" ] || {
    echo "mock-session: cannot realise $1" >&2
    exit 1
  }
  echo "$p"
}

SWAY=$(need sway)
XDP=$(need xdg-desktop-portal)
XDP_WLR=$(need xdg-desktop-portal-wlr)
XDP_KDE=$(need kdePackages.xdg-desktop-portal-kde)
KWIN=$(need kdePackages.kwin)
DBUS=$(need dbus)
PIPEWIRE=$(need pipewire)
WIREPLUMBER=$(need wireplumber)
PULSEAUDIO=$(need pulseaudio)
FFMPEG=$(need ffmpeg)
GST=$(need gst_all_1.gstreamer)
GST_GOOD=$(need gst_all_1.gst-plugins-good)
GST_BASE=$(need gst_all_1.gst-plugins-base)

ROOT=$(mktemp -d /tmp/recgo-mock-XXXXXX)
export XDG_RUNTIME_DIR="$ROOT/run"
mkdir -p "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
export XDG_CONFIG_HOME="$ROOT/config"
mkdir -p "$XDG_CONFIG_HOME"
export XDG_DATA_HOME="$ROOT/data"
mkdir -p "$XDG_DATA_HOME"
export XDG_CACHE_HOME="$ROOT/cache"
mkdir -p "$XDG_CACHE_HOME"
export XDG_CURRENT_DESKTOP
export WAYLAND_DISPLAY=wayland-mock

PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null; done
  sleep 0.3
  for pid in "${PIDS[@]:-}"; do kill -9 "$pid" 2>/dev/null; done
  rm -rf "$ROOT"
}
trap cleanup EXIT

log() { echo "[mock:$FLAVOUR] $*" >&2; }

eval "$("$DBUS/bin/dbus-launch" --sh-syntax)"
PIDS+=("$DBUS_SESSION_BUS_PID")
log "session bus $DBUS_SESSION_BUS_ADDRESS"

"$PIPEWIRE/bin/pipewire" >"$ROOT/pipewire.log" 2>&1 &
PIDS+=($!)
sleep 1
"$PIPEWIRE/bin/pipewire-pulse" >"$ROOT/pipewire-pulse.log" 2>&1 &
PIDS+=($!)
"$WIREPLUMBER/bin/wireplumber" >"$ROOT/wireplumber.log" 2>&1 &
PIDS+=($!)
sleep 1

if "$PULSEAUDIO/bin/pactl" load-module module-null-sink \
  sink_name=recgo_mock sink_properties=device.description=RecgoMockSink \
  >"$ROOT/nullsink.log" 2>&1; then
  "$PULSEAUDIO/bin/pactl" set-default-source recgo_mock.monitor >/dev/null 2>&1
  log "virtual capture source: recgo_mock.monitor"
else
  log "WARNING: could not create a null sink; audio checks will be skipped"
fi

case "$FLAVOUR" in
  sway)
    XDG_CURRENT_DESKTOP=sway
    cat >"$ROOT/sway.conf" <<'CONF'
# Headless sway: the backend supplies one virtual output, so there is nothing
# to configure beyond keeping the session quiet.
CONF
    unset WAYLAND_DISPLAY
    WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 \
      "$SWAY/bin/sway" -c "$ROOT/sway.conf" >"$ROOT/sway.log" 2>&1 &
    PIDS+=($!)
    IMPL_BIN="$XDP_WLR/libexec/xdg-desktop-portal-wlr"
    IMPL_NAME=wlr
    IMPL_SHARE="$XDP_WLR/share"

    mkdir -p "$XDG_CONFIG_HOME/xdg-desktop-portal-wlr"
    cat >"$XDG_CONFIG_HOME/xdg-desktop-portal-wlr/config" <<'CONF'
[screencast]
chooser_type=none
output_name=HEADLESS-1
CONF
    ;;
  kde)
    XDG_CURRENT_DESKTOP=KDE
    "$KWIN/bin/kwin_wayland" --virtual --width 1280 --height 800 \
      --no-global-shortcuts --socket "$WAYLAND_DISPLAY" \
      >"$ROOT/kwin.log" 2>&1 &
    PIDS+=($!)
    IMPL_BIN="$XDP_KDE/libexec/xdg-desktop-portal-kde"
    IMPL_NAME=kde
    IMPL_SHARE="$XDP_KDE/share"
    ;;
  *)
    echo "unknown flavour $FLAVOUR" >&2
    exit 1
    ;;
esac

for _ in $(seq 1 80); do
  sock=$(find "$XDG_RUNTIME_DIR" -maxdepth 1 -type s -name 'wayland-*' 2>/dev/null | head -1)
  [ -n "$sock" ] && break
  sleep 0.25
done
if [ -z "${sock:-}" ]; then
  log "compositor never created a wayland socket in $XDG_RUNTIME_DIR"
  tail -25 "$ROOT/${FLAVOUR}.log" >&2 2>/dev/null || true
  exit 1
fi
export WAYLAND_DISPLAY="$(basename "$sock")"
log "compositor up on $WAYLAND_DISPLAY"

export XDG_DATA_DIRS="$IMPL_SHARE:$XDP/share:${XDG_DATA_DIRS:-/usr/share}"
mkdir -p "$XDG_CONFIG_HOME/xdg-desktop-portal"
cat >"$XDG_CONFIG_HOME/xdg-desktop-portal/portals.conf" <<CONF
[preferred]
default=$IMPL_NAME
org.freedesktop.impl.portal.ScreenCast=$IMPL_NAME
CONF

[ -x "$IMPL_BIN" ] || IMPL_BIN=$(find "${IMPL_BIN%/*/*}" -name "xdg-desktop-portal-$IMPL_NAME" -type f 2>/dev/null | head -1)
"$IMPL_BIN" >"$ROOT/impl.log" 2>&1 &
PIDS+=($!)
sleep 1

"$XDP/libexec/xdg-desktop-portal" >"$ROOT/xdp.log" 2>&1 &
PIDS+=($!)

for _ in $(seq 1 60); do
  "$DBUS/bin/dbus-send" --session --print-reply --dest=org.freedesktop.DBus \
    /org/freedesktop/DBus org.freedesktop.DBus.NameHasOwner \
    string:org.freedesktop.portal.Desktop 2>/dev/null | grep -q 'boolean true' && break
  sleep 0.25
done
log "portal impl=$IMPL_NAME"

export PATH="$PULSEAUDIO/bin:$FFMPEG/bin:$DBUS/bin:$GST/bin:$PATH"
export GST_PLUGIN_SYSTEM_PATH_1_0="$GST/lib/gstreamer-1.0:$GST_GOOD/lib/gstreamer-1.0:$GST_BASE/lib/gstreamer-1.0:$PIPEWIRE/lib/gstreamer-1.0"
export RECGO_MOCK_ROOT="$ROOT"
export RECGO_MOCK_FLAVOUR="$FLAVOUR"
"$@"
rc=$?

if [ $rc -ne 0 ]; then
  log "command failed ($rc); logs:"
  for f in impl xdp "$FLAVOUR" pipewire pipewire-pulse wireplumber nullsink; do
    [ -s "$ROOT/$f.log" ] && {
      echo "--- $f.log ---" >&2
      tail -25 "$ROOT/$f.log" >&2
    }
  done
fi
exit $rc
