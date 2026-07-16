#!/bin/sh
# Build the Theos rootless app package and install it on the device.
#
# Requires the standard ssh-iphone / scp-iphone wrappers.

set -e
cd "$(dirname "$0")"

HELPER_BACKUP=""
cleanup() {
  if [ -n "$HELPER_BACKUP" ] && [ -f "$HELPER_BACKUP" ]; then
    cp "$HELPER_BACKUP" Resources/helper.arm64
    rm -f "$HELPER_BACKUP"
  fi
}
trap cleanup EXIT INT TERM

copy_to_iphone() {
  if command -v scp-iphone >/dev/null 2>&1; then
    scp-iphone "$1" "$2"
  else
    scp "$1" "iphone:$2"
  fi
}

run_on_iphone() {
  if command -v ssh-iphone >/dev/null 2>&1; then
    ssh-iphone "$1"
  else
    "$HOME/bin/ssh-iphone" "$1"
  fi
}

mkdir -p Resources
THEOS="${THEOS:-$HOME/theos}"
GO="${GO:-/usr/local/go/bin/go}"
[ -x "$GO" ] || GO=go
IOS_SDK=$(ls -d "$THEOS"/sdks/iPhoneOS*.sdk 2>/dev/null | sort -V | tail -1)
[ -n "$IOS_SDK" ] || { echo "no iPhoneOS SDK found under $THEOS/sdks" >&2; exit 1; }
IOS_SDK_VERSION=$(basename "$IOS_SDK" | sed 's/iPhoneOS//; s/\.sdk//')
if [ -x "$THEOS/toolchain/linux/host/bin/clang-13" ]; then
  IOS_CLANG="$THEOS/toolchain/linux/host/bin/clang-13"
  IOS_LDFLAGS="-fuse-ld=lld -Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION"
elif command -v xcrun >/dev/null 2>&1; then
  IOS_CLANG=$(xcrun -f clang)
  IOS_LDFLAGS="-Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION"
else
  echo "no iOS-capable clang found (checked Theos Linux toolchain and xcrun)" >&2
  exit 1
fi
IOS_CC="$IOS_CLANG -target arm64-apple-ios15.0 -isysroot $IOS_SDK -Wno-incompatible-sysroot -Wno-unused-command-line-argument"
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$IOS_CC" \
  CGO_LDFLAGS="$IOS_LDFLAGS" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/Resources/appstore-helper.arm64 ./cmd/ipadecrypt-appstore-helper )
ldid -Sappstore-helper.entitlements.plist Resources/appstore-helper.arm64
chmod +x Resources/appstore-helper.arm64
HELPER_BACKUP=$(mktemp)
cp Resources/helper.arm64 "$HELPER_BACKUP"
ldid -S../helper/entitlements.plist Resources/helper.arm64
chmod +x Resources/helper.arm64

mkdir -p layout/usr/libexec
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$IOS_CC" \
  CGO_LDFLAGS="$IOS_LDFLAGS" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/layout/usr/libexec/ipadecryptd ./cmd/ipadecryptd )
ldid -Sdaemon.entitlements.plist layout/usr/libexec/ipadecryptd
chmod +x layout/usr/libexec/ipadecryptd
chmod +x layout/DEBIAN/postinst

THEOS="$THEOS" make package FINALPACKAGE=1

DEB=$(ls -t packages/com.korboy.ipadecrypt_*.deb | head -1)
[ -n "$DEB" ] || { echo "no .deb produced" >&2; exit 1; }

REMOTE="/var/mobile/Documents/Downloads/$(basename "$DEB")"
copy_to_iphone "$DEB" "$REMOTE"
run_on_iphone "dpkg -r com.korboy.ipadecrypt-app 2>/dev/null || true; dpkg -i '$REMOTE' && dpkg -s com.korboy.ipadecrypt | grep -q '^Status: install ok installed' && uicache -p /var/jb/Applications/ipadecrypt.app && open com.korboy.ipadecrypt"
echo "installed - app path: /var/jb/Applications/ipadecrypt.app"
