#!/bin/sh
# Build the Theos rootless app package and install it on the device.
#
# Requires the standard ssh-iphone / scp-iphone wrappers.

set -e
cd "$(dirname "$0")"

mkdir -p Resources
THEOS="${THEOS:-$HOME/theos}"
GO="${GO:-/usr/local/go/bin/go}"
[ -x "$GO" ] || GO=go
IOS_SDK=$(ls -d "$THEOS"/sdks/iPhoneOS*.sdk 2>/dev/null | sort -V | tail -1)
[ -n "$IOS_SDK" ] || { echo "no iPhoneOS SDK found under $THEOS/sdks" >&2; exit 1; }
IOS_SDK_VERSION=$(basename "$IOS_SDK" | sed 's/iPhoneOS//; s/\.sdk//')
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$THEOS/toolchain/linux/host/bin/clang-13 -target arm64-apple-ios15.0 -isysroot $IOS_SDK -Wno-incompatible-sysroot -Wno-unused-command-line-argument" \
  CGO_LDFLAGS="-fuse-ld=lld -Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/Resources/appstore-helper.arm64 ./cmd/ipadecrypt-appstore-helper )
ldid -Sappstore-helper.entitlements.plist Resources/appstore-helper.arm64
chmod +x Resources/appstore-helper.arm64
ldid -S../helper/entitlements.plist Resources/helper.arm64
chmod +x Resources/helper.arm64

mkdir -p layout/usr/libexec
( cd .. && \
  GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$THEOS/toolchain/linux/host/bin/clang-13 -target arm64-apple-ios15.0 -isysroot $IOS_SDK -Wno-incompatible-sysroot -Wno-unused-command-line-argument" \
  CGO_LDFLAGS="-fuse-ld=lld -Wl,-platform_version,ios,15.0,$IOS_SDK_VERSION" \
  "$GO" build -trimpath -ldflags="-s -w" -o app/layout/usr/libexec/ipadecryptd ./cmd/ipadecryptd )
ldid -Sdaemon.entitlements.plist layout/usr/libexec/ipadecryptd
chmod +x layout/usr/libexec/ipadecryptd
chmod +x layout/DEBIAN/postinst

THEOS="$THEOS" make package FINALPACKAGE=1

DEB=$(ls -t packages/com.korboy.ipadecrypt_*.deb | head -1)
[ -n "$DEB" ] || { echo "no .deb produced" >&2; exit 1; }

REMOTE="/var/mobile/Documents/Downloads/$(basename "$DEB")"
scp-iphone "$DEB" "$REMOTE"
ssh-iphone "dpkg -r com.korboy.ipadecrypt-app 2>/dev/null || true; dpkg -i '$REMOTE' && dpkg -s com.korboy.ipadecrypt | grep -q '^Status: install ok installed' && uicache -p /var/jb/Applications/ipadecrypt.app && open com.korboy.ipadecrypt"
echo "installed - app path: /var/jb/Applications/ipadecrypt.app"
