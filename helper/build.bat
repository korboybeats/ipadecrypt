@echo off
rem Build ipadecrypt-helper-arm64 inside the pinned Docker toolchain to ensure
rem consistent build across platforms.
rem To build the toolchain locally set IPADECRYPT_TOOLCHAIN_IMAGE=build.

setlocal EnableExtensions EnableDelayedExpansion
cd /d "%~dp0\.."

where docker >nul 2>nul
if errorlevel 1 (
    echo docker is required 1>&2
    exit /b 1
)

if "%IPADECRYPT_TOOLCHAIN_IMAGE%"=="" (
    set "IMAGE=ghcr.io/londek/ipadecrypt-toolchain:latest"
) else (
    set "IMAGE=%IPADECRYPT_TOOLCHAIN_IMAGE%"
)

if "%IMAGE%"=="build" (
    set "IMAGE=ipadecrypt-toolchain:local"
    echo ==^> building toolchain locally from helper/Dockerfile
    docker build ^
        --load ^
        --platform linux/amd64 ^
        --provenance=false ^
        --sbom=false ^
        -t "!IMAGE!" ^
        helper/
    if errorlevel 1 exit /b 1
) else (
    docker image inspect --format "{{.Id}}" "!IMAGE!" >nul 2>nul
    if errorlevel 1 (
        echo ==^> pulling toolchain image ^(!IMAGE!^)
        docker pull --platform linux/amd64 "!IMAGE!"
        if errorlevel 1 exit /b 1
    ) else (
        echo ==^> toolchain image already present locally ^(!IMAGE!^)
    )
)

if not exist helper\dist mkdir helper\dist

echo ==^> compiling ipadecrypt-helper-arm64 in container
docker run --rm ^
    --platform linux/amd64 ^
    -v "%CD%:/workspace" ^
    -w /workspace ^
    "%IMAGE%" ^
    /bin/sh -c "set -e; clang -target arm64-apple-ios${IPHONEOS_DEPLOYMENT_TARGET} -isysroot $IPHONEOS_SDK -isystem $IPHONEOS_SDK/usr/include -L $IPHONEOS_SDK/usr/lib -fuse-ld=lld -Wl,-arch,arm64 -Wl,-platform_version,ios,$IPHONEOS_DEPLOYMENT_TARGET,$IPHONEOS_SDK_VERSION -Wno-incompatible-sysroot -O2 -fno-stack-protector -Wno-deprecated-declarations -no-canonical-prefixes -o helper/dist/ipadecrypt-helper-arm64 helper/helper.c; ldid -Shelper/entitlements.plist helper/dist/ipadecrypt-helper-arm64"
if errorlevel 1 exit /b 1

echo ok: helper/dist/ipadecrypt-helper-arm64
