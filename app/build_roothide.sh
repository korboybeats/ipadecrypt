#!/bin/sh
# Build the Theos RootHide app package and upload it to the device.
# Pass --no-install (or IPADECRYPT_NO_INSTALL=1) to only build the .deb.

set -e
cd "$(dirname "$0")"

NO_INSTALL="${IPADECRYPT_NO_INSTALL:-0}"
[ "${1:-}" = "--no-install" ] && NO_INSTALL=1

HELPER_BACKUP=""
TMP_HOME=""
cleanup() {
  if [ -n "$HELPER_BACKUP" ] && [ -f "$HELPER_BACKUP" ]; then
    cp "$HELPER_BACKUP" Resources/helper.arm64
    rm -f "$HELPER_BACKUP"
  fi
  if [ -n "$TMP_HOME" ] && [ -d "$TMP_HOME" ]; then
    rm -rf "$TMP_HOME"
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

mkdir -p Resources
THEOS="${THEOS_ROOTHIDE:-$HOME/theos-roothide}"
GO="${GO:-/usr/local/go/bin/go}"
[ -x "$GO" ] || GO=go
# IOS_SDK may be set explicitly; otherwise use the newest SDK under $THEOS/sdks
# that actually resolves (dangling symlinks are skipped), then ~/theos/sdks.
if [ -z "${IOS_SDK:-}" ]; then
  for d in $(ls -d "$THEOS"/sdks/iPhoneOS*.sdk "$HOME"/theos/sdks/iPhoneOS*.sdk 2>/dev/null | sort -V -r); do
    if [ -d "$d/usr/include" ]; then IOS_SDK="$d"; break; fi
  done
fi
[ -n "${IOS_SDK:-}" ] && [ -d "$IOS_SDK" ] || { echo "no usable iPhoneOS SDK found under $THEOS/sdks (set IOS_SDK)" >&2; exit 1; }
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

# appstore-helper runs Apple's SAP/AMS code under Unicorn, which must be
# statically linked on iOS (see build_unicorn_ios.sh).
UNICORN_IOS_LIB="${UNICORN_IOS_LIB:-$(./build_unicorn_ios.sh)}"
[ -f "$UNICORN_IOS_LIB" ] || { echo "Unicorn iOS archive not found: $UNICORN_IOS_LIB" >&2; exit 1; }
HELPER_LDFLAGS="$IOS_LDFLAGS -Wl,-force_load,$UNICORN_IOS_LIB -Wl,-export_dynamic"

( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$IOS_CC" \
  CGO_LDFLAGS="$HELPER_LDFLAGS" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/Resources/appstore-helper.arm64 ./cmd/ipadecrypt-appstore-helper )
ldid -Sappstore-helper.entitlements.plist Resources/appstore-helper.arm64
chmod +x Resources/appstore-helper.arm64

HELPER_SRC=../internal/device/ipadecrypt-helper-arm64
[ -f ../helper/dist/ipadecrypt-helper-arm64 ] && HELPER_SRC=../helper/dist/ipadecrypt-helper-arm64
if [ -f "$HELPER_SRC" ]; then
  HELPER_BACKUP=$(mktemp)
  cp Resources/helper.arm64 "$HELPER_BACKUP"
  cp "$HELPER_SRC" Resources/helper.arm64
  ldid -S../helper/entitlements.plist Resources/helper.arm64
  chmod +x Resources/helper.arm64
fi

mkdir -p layout/usr/libexec
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$IOS_CC" \
  CGO_LDFLAGS="$IOS_LDFLAGS" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/layout/usr/libexec/ipadecryptd ./cmd/ipadecryptd )
ldid -Sdaemon.entitlements.plist layout/usr/libexec/ipadecryptd
chmod +x layout/usr/libexec/ipadecryptd
chmod +x layout/DEBIAN/postinst layout/usr/libexec/ipadecryptd-supervisor

TMP_HOME=$(mktemp -d)
env -u THEOS_DEVICE_IP -u THEOS_DEVICE_PORT -u THEOS_DEVICE_USER \
  HOME="$TMP_HOME" XDG_CONFIG_HOME="$TMP_HOME/.config" \
  THEOS="$THEOS" make clean package FINALPACKAGE=1 THEOS_PACKAGE_SCHEME=roothide SYSROOT="$IOS_SDK"

DEB=$(ls -t packages/com.korboy.ipadecrypt_*_iphoneos-arm64e.deb | head -1)
[ -n "$DEB" ] || { echo "no RootHide .deb produced" >&2; exit 1; }
echo "built RootHide package: $DEB"

if [ "$NO_INSTALL" = "1" ]; then
  exit 0
fi

REMOTE="/var/mobile/Documents/Downloads/$(basename "$DEB")"
copy_to_iphone "$DEB" "$REMOTE"
echo "uploaded RootHide package: $REMOTE"
