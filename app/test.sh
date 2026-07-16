#!/bin/sh

set -eu
cd "$(dirname "$0")/.."

out=$(mktemp -t ipadecrypt-install-policy)
trap 'rm -f "$out"' EXIT INT TERM

xcrun clang -std=c11 -Iapp/Models \
  app/Tests/IDAppStoreInstallPolicyTests.c \
  app/Models/IDAppStoreInstallPolicy.c \
  -o "$out"
"$out"
