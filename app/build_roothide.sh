#!/bin/sh
# Build the Theos RootHide app package and upload it to the device.

set -e
cd "$(dirname "$0")"

mkdir -p Resources
THEOS="${THEOS_ROOTHIDE:-$HOME/theos-roothide}"
GO="${GO:-/usr/local/go/bin/go}"
[ -x "$GO" ] || GO=go
IOS_SDK=$(ls -d "$THEOS"/sdks/iPhoneOS*.sdk 2>/dev/null | sort -V | tail -1)
[ -n "$IOS_SDK" ] || { echo "no iPhoneOS SDK found under $THEOS/sdks" >&2; exit 1; }
IOS_SDK_VERSION=$(basename "$IOS_SDK" | sed 's/iPhoneOS//; s/\.sdk//')

( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$THEOS/toolchain/linux/host/bin/clang-13 -target arm64-apple-ios15.0 -isysroot $IOS_SDK -Wno-incompatible-sysroot -Wno-unused-command-line-argument" \
  CGO_LDFLAGS="-fuse-ld=lld -Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION" \
  "$GO" build -trimpath -ldflags="-s -w -X main.defaultRootDir=/rootfs/var/mobile/Documents/ipadecrypt" -o app/Resources/appstore-helper.arm64 ./cmd/ipadecrypt-appstore-helper )
ldid -Sappstore-helper.entitlements.plist Resources/appstore-helper.arm64
chmod +x Resources/appstore-helper.arm64

if [ -f ../helper/dist/ipadecrypt-helper-arm64 ]; then
  cp ../helper/dist/ipadecrypt-helper-arm64 Resources/helper.arm64
  ldid -S../helper/entitlements.plist Resources/helper.arm64
  chmod +x Resources/helper.arm64
fi

mkdir -p layout/usr/libexec
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$THEOS/toolchain/linux/host/bin/clang-13 -target arm64-apple-ios15.0 -isysroot $IOS_SDK -Wno-incompatible-sysroot -Wno-unused-command-line-argument" \
  CGO_LDFLAGS="-fuse-ld=lld -Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/layout/usr/libexec/ipadecryptd ./cmd/ipadecryptd )
ldid -Sdaemon.entitlements.plist layout/usr/libexec/ipadecryptd
chmod +x layout/usr/libexec/ipadecryptd
chmod +x layout/DEBIAN/postinst layout/usr/libexec/ipadecryptd-supervisor

TMP_HOME=$(mktemp -d)
trap 'rm -rf "$TMP_HOME"' EXIT
env -u THEOS_DEVICE_IP -u THEOS_DEVICE_PORT -u THEOS_DEVICE_USER \
  HOME="$TMP_HOME" XDG_CONFIG_HOME="$TMP_HOME/.config" \
  THEOS="$THEOS" make clean package FINALPACKAGE=1 THEOS_PACKAGE_SCHEME=roothide

DEB=$(ls -t packages/com.korboy.ipadecrypt_*_iphoneos-arm64e.deb | head -1)
[ -n "$DEB" ] || { echo "no RootHide .deb produced" >&2; exit 1; }
echo "built RootHide package: $DEB"

REMOTE="/var/mobile/Documents/Downloads/$(basename "$DEB")"
scp-iphone "$DEB" "$REMOTE"
echo "uploaded RootHide package: $REMOTE"
