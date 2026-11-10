# Building ipadecrypt

ipadecrypt has three parts:

- **`ipadecrypt` CLI** - Go binary that runs on your computer.
- **Helper** - a small arm64 iOS binary (`ipadecrypt-helper-arm64`) that runs on the jailbroken device. Consumed by the CLI using `go:embed`.

For regular development you only need the CLI; the prebuilt helper is committed at `internal/device/ipadecrypt-helper-arm64`. Rebuilding the helper requires Docker.
The optional offline mode (`ipadecrypt mint` + `decrypt --offline`) also embeds
a device mint bundle under `internal/credmint/bundle/` (three signed arm64 iOS
programs plus two data blobs). It is committed, so a plain `go build` is
mint-capable; see [`internal/credmint/toolchain/README.md`](internal/credmint/toolchain/README.md)
to rebuild it. The offline App Store engine needs a 64-bit host, so there are
no 32-bit (`386`) CLI builds.
- **`ipadecrypt` CLI** - Go binary that runs on your computer. This is all you
  need for everyday development and for decrypting apps.
- **Device mint bundle** - three small signed arm64 iOS programs
  (`credential.ios`, `driver.ios`, `trigger.ios`) plus two data blobs, embedded
  into the CLI with `go:embed` and pushed to a jailbroken device only during the
  one-time `bootstrap` credential mint.
- **On-device decrypt helper** - a signed arm64 iOS program
  (`internal/device/ipadecrypt-helper-arm64`, `go:embed`ed from the C sources in
  `helper/`). Pushed to a jailbroken device only for the on-device fallback that
  decrypts old apps still shipping a 32-bit slice, which the arm64-only offline
  decryptor can't handle.

## Prerequisites

### CLI only (cross-platform)

- Go 1.25+

### Helper

The helper is built inside a prebuilt Docker image published at `ghcr.io/londek/ipadecrypt-toolchain:latest` (~750 MB). The image is defined by [`helper/Dockerfile`](helper/Dockerfile) and pushed by the `toolchain` job in [`.github/workflows/ci.yml`](.github/workflows/ci.yml) on every CI run - tagged by the Dockerfile's hash, plus `:latest` on `main`.

What's inside:

- `clang` + `lld` (from [apt.llvm.org](https://apt.llvm.org))
- [`ldid`](https://github.com/ProcursusTeam/ldid) (Procursus fork) for ad-hoc iOS code signing
- iPhoneOS SDK from [`xybp888/iOS-SDKs`](https://github.com/xybp888/iOS-SDKs), trimmed to what this project needs

The image is version-pinned via `ARG`s at the top of the Dockerfile (`LLVM_VERSION`, `LDID_REV`, `SDKS_COMMIT`, `SDK_VERSION`, `IOS_DEPLOYMENT_TARGET`). Because the build always runs on `linux/amd64` - via Rosetta / QEMU on Apple Silicon, natively on Linux - the compiler binary is the same bytes everywhere, and so is its output. That's what makes the drift check meaningful.

## Build the CLI

```sh
go build -o ipadecrypt ./cmd/ipadecrypt
```

For an optimized release-style build:

```sh
go build -trimpath -ldflags="-s -w" -o ipadecrypt ./cmd/ipadecrypt
```

This reuses `internal/device/ipadecrypt-helper-arm64` from the repo - no iOS toolchain needed.

## Build the helper

Only needed when you change `helper/helper.c` or `helper/entitlements.plist`.

```sh
./helper/build.sh
```

On Windows CMD:

```bat
helper\build.bat
```
Produces `helper/dist/ipadecrypt-helper-arm64`. Copy it into place for `go:embed`:
This compiles the three `*.ios` programs, signs them, copies them and the data
blobs into `internal/credmint/bundle/`, and fails if any program is still a
placeholder. Commit the refreshed bundle. The build is byte-reproducible, and
the [`credmint bundle drift`](.github/workflows/credmint-bundle.yml) CI job
rebuilds from the pinned sources and byte-compares against the committed bundle.
## Rebuild the on-device decrypt helper
Only needed when the C sources under `helper/` change. Like the mint bundle it
builds on **Linux or macOS via Docker** (no Xcode) in a pinned toolchain image -
`clang` targeting `arm64-apple-ios`, an iPhoneOS SDK, and `ldid` for ad-hoc
signing with `helper/entitlements.plist`.
```sh
# uses/pulls the published toolchain image:
./helper/build.sh
# or build the toolchain image locally from helper/Dockerfile first:
IPADECRYPT_TOOLCHAIN_IMAGE=build ./helper/build.sh
This writes `helper/dist/ipadecrypt-helper-arm64`. Copy it over the embedded
copy and commit it:

```sh
cp helper/dist/ipadecrypt-helper-arm64 internal/device/ipadecrypt-helper-arm64
```

## The drift check

CI (see [`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs the exact same `./helper/build.sh` - same Dockerfile, same pinned toolchain, same container - and byte-compares the fresh output against the committed `internal/device/ipadecrypt-helper-arm64`. If they don't match, the PR fails with `ipadecrypt-helper-arm64 drift detected`.
In practice this means: **if you touch `helper.c` or `entitlements.plist`, rebuild and commit the new `internal/device/ipadecrypt-helper-arm64` in the same PR**. If you don't, CI catches it.
The drift is meaningful because the canonical build environment is reproducible, not because we got lucky - the whole point of the container is that there's no difference between your build and CI's.
The committed binary matches the `helper/` sources, so a plain `go build` is
fully decrypt-capable without this step.
