#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

HELPERS=1 INSTALL=0
for arg in "$@"; do
  case "$arg" in
    --no-helpers) HELPERS=0 ;;
    --install) INSTALL=1 ;;
    *)
      echo "unknown flag: $arg" >&2
      exit 2
      ;;
  esac
done

swift build -c release

if [[ ! -f .build/AppIcon.icns || icon.swift -nt .build/AppIcon.icns ]]; then
  swift icon.swift .build
  iconutil -c icns .build/AppIcon.iconset -o .build/AppIcon.icns
fi

APP=.build/Recgo.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources"
cp Info.plist "$APP/Contents/Info.plist"
cp .build/release/Recgo "$APP/Contents/MacOS/Recgo"
cp .build/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"

if [[ "$HELPERS" == 1 ]]; then
  export LIBRARY_PATH="${LIBRARY_PATH:-$(xcrun --show-sdk-path)/usr/lib}"
  for cli in recgo recgo-tab recgo-browser recgo-desktop recgo-window recgo-sessions recgo-meet-watch; do
    (cd .. && go build -o "mac/$APP/Contents/Helpers/$cli" "./cmd/$cli")
  done
fi

codesign --force --deep --sign - "$APP"
echo "built $PWD/$APP"

if [[ "$INSTALL" == 1 ]]; then
  mkdir -p "$HOME/Applications"
  rm -rf "$HOME/Applications/Recgo.app"
  ditto "$APP" "$HOME/Applications/Recgo.app"
  echo "installed $HOME/Applications/Recgo.app"
fi
