#!/bin/sh

set -eu
cd "$(dirname "$0")/.."

install_out=$(mktemp -t ipadecrypt-install-policy)
auth_out=$(mktemp -t ipadecrypt-auth-state)
trap 'rm -f "$install_out" "$auth_out"' EXIT INT TERM

xcrun clang -std=c11 -Iapp/Models \
  app/Tests/IDAppStoreInstallPolicyTests.c \
  app/Models/IDAppStoreInstallPolicy.c \
  -o "$install_out"
"$install_out"

xcrun clang -std=c11 -Iapp/Models \
  app/Tests/IDAppleAuthStateTests.c \
  app/Models/IDAppleAuthState.c \
  -o "$auth_out"
"$auth_out"
