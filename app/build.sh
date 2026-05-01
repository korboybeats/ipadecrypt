#!/bin/sh
# Build a TrollStore-installable .tipa from the theos app project and push
# it to the device.
#
# Requires the standard ssh-iphone / scp-iphone wrappers.

set -e
cd "$(dirname "$0")"

THEOS="${THEOS:-$HOME/theos}" make package FINALPACKAGE=1

DEB=$(ls -t packages/com.korboy.ipadecrypt-app_*.deb | head -1)
[ -n "$DEB" ] || { echo "no .deb produced" >&2; exit 1; }

# Convert .deb → .tipa: theos rootless apps install under /var/jb/Applications
# but TrollStore expects an IPA whose Payload/ contains the .app directly.
WORK=$(mktemp -d)
trap "rm -rf $WORK" EXIT
dpkg-deb -x "$DEB" "$WORK/extracted"

APP_DIR=$(find "$WORK/extracted" -maxdepth 6 -name '*.app' -type d | head -1)
[ -n "$APP_DIR" ] || { echo "no .app inside .deb" >&2; exit 1; }

mkdir -p "$WORK/Payload"
cp -R "$APP_DIR" "$WORK/Payload/"

TIPA="packages/$(basename "${DEB%.deb}").tipa"
( cd "$WORK" && zip -qr "$OLDPWD/$TIPA" Payload )
echo "wrote $TIPA"

scp-iphone "$TIPA" "/var/mobile/Documents/Downloads/$(basename "$TIPA")"
ssh-iphone "trollinst /var/mobile/Documents/Downloads/$(basename "$TIPA")"
echo "installed - launch with: ssh-iphone \"open com.korboy.ipadecrypt\""
