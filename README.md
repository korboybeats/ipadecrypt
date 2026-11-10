<div align="center">

<img height="50px" src="https://readme-typing-svg.herokuapp.com?font=Inter&weight=700&size=36&color=FFFFFF&center=true&vCenter=true&width=300&lines=ipadecrypt&repeat=false&duration=2500" alt="ipadecrypt">

**End-to-end FairPlay decrypter for App Store apps.**
*Give it a bundle ID, get a decrypted `.ipa`. And yes - it happily decrypts iOS 26 apps.*

[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go)](https://golang.org/)
[![macOS](https://img.shields.io/badge/macOS-000?style=flat-square&logo=apple&logoColor=white)](#install)
[![Linux](https://img.shields.io/badge/Linux-000?style=flat-square&logo=linux&logoColor=white)](#install)
[![Windows](https://img.shields.io/badge/Windows-000?style=flat-square&logo=data:image/svg%2Bxml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAyNCAyNCI+PHBhdGggZmlsbD0id2hpdGUiIGQ9Ik0wIDBoMTF2MTFIMHpNMTMgMGgxMXYxMUgxM3pNMCAxM2gxMXYxMUgwek0xMyAxM2gxMXYxMUgxM3oiLz48L3N2Zz4=)](#install)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](#license)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen?style=flat-square)](https://github.com/londek/ipadecrypt/pulls)

<img width="90%" src="https://github.com/user-attachments/assets/ba8dbd32-a2fb-49cc-afee-3aa88050718e" />

</div>

## Fork changes (korboybeats)

This fork adds a handful of QoL features on top of upstream:

- **SSH port + multi-host failover.** Bootstrap prompts for a non-default SSH port. `host` accepts a comma-separated list of IPs; the first reachable one is used (handy if your phone hops between Wi-Fi networks).
- **Fuzzy target resolution.** `ipadecrypt decrypt messenger` scans installed apps and matches against bundle ID + display name. Single match auto-selects; multiple matches prompt to pick.
- **On-device StoreKit download path.** New menu option that triggers the device's own App Store flow via `SKUIItem` + `SKUIItemStateCenter`. Apple's CDN serves the latest version compatible with the device's iOS. The download uses `STDRDL` (redownload-from-library) pricing, which skips the Face ID purchase confirmation. ipadecrypt then decrypts the freshly-installed bundle.
- **Jailbreak app.** Offers **Latest from App Store** and **Latest iOS-compatible** actions, copyable logs, IPA sharing, and a Filza shortcut for the decrypted output folder. The app can be packaged for rootless or RootHide from the same source tree.
- **Auto-confirm tweak (`ipadecryptautoalert`).** Optional SpringBoard tweak installed during bootstrap via `dpkg`. When ipadecrypt starts a **Latest iOS-compatible** StoreKit download, it arms a short-lived sentinel file; the tweak only auto-taps the `Download` action on the App Store older-version alert while that sentinel is valid.
- **Configurable decrypted IPA retention.** Choose whether CLI decrypt output is kept on the desktop, device, or both.
- **Single PC workspace.** CLI config, cookies, cache, and logs live under `~/ipadecrypt/`; decrypted PC outputs default to `~/ipadecrypt/decrypted/`.
- **RootHide-aware device workspace.** RootHide device files are kept under `/rootfs/private/var/mobile/Documents/ipadecrypt/`.
- **Faster IPA post-processing.** Metadata/Watch cleanup is combined into one scanned pass and skips rewriting entirely when there is nothing to remove. Cryptid verification streams Mach-O load commands instead of reading whole binaries into memory.
- **~60× faster install check.** Replaced the per-file shell loop with a single `grep` over all top-level Info.plists.
- **Optional offline decryption (from upstream).** `ipadecrypt mint` extracts a reusable per-account FairPlay credential over root SSH once; after that `ipadecrypt decrypt --offline <target>` downloads and decrypts on the computer with no device in the loop. The device-based flow above stays the default.
- **Short command flags.** `-d` (decrypt), `-b` (bootstrap), `-v` (versions), `-dl` (download), `-a`/`auth` (refresh Apple ID auth), `-k` (keep policy), `-u` (update).

The offline decryptor handles **arm64** slices (everything the modern App Store
ships). A few old builds still ship a **32-bit (armv7) slice** - those can't be
decrypted offline, so ipadecrypt automatically falls back to decrypting them
**on your jailbroken device** when one is configured (the same device used for
minting). Pass `--no-on-device` to disable the fallback, or `--on-device` to go
straight to the device.

## Requirements

### On your computer
- Jailbroken iPhone reachable over the network

### On the jailbroken iPhone
All installable through Sileo:
| Package | Purpose |
|---|---|
| **OpenSSH** | SSH server - ipadecrypt drives the device over SSH |
| **AppSync Unified** | Bypasses installd's signature check (add repo `https://lukezgd.github.io/repo`) |
| **appinst** | Installs modified IPAs on the device (add repo `https://lukezgd.github.io/repo`) |
- Nothing special for everyday (arm64) decrypting. A jailbroken iPhone reachable
  over the network (or over USB with `iproxy`) is needed for the one-time mint,
  and again only for the on-device fallback on old 32-bit apps.
| Requirement | Purpose |
| **OpenSSH with root access** | The mint and the on-device fallback connect over root SSH (install OpenSSH from Sileo) |
| **≥1 owned App Store app installed** | Bought with the same Apple ID; used to trigger FairPlay once during the mint |
| **AppSync Unified + `appinst`** | Only for the on-device fallback: installs the app on the device so it can be decrypted |

> Tested on palera1n rootless, Dopamine rootless, and RootHide. iOS 14 through 17 on A10–A14 devices are expected to work.

## Install

This fork is intended to live under `~/ipadecrypt/src`, with runtime files
under `~/ipadecrypt/` and decrypted IPAs under `~/ipadecrypt/decrypted/`.

Clone the fork:

```sh
mkdir -p ~/ipadecrypt
git clone https://github.com/korboybeats/ipadecrypt ~/ipadecrypt/src
cd ~/ipadecrypt/src
```

Build and install the CLI:

```sh
go build -trimpath -ldflags="-s -w" -o ipadecrypt ./cmd/ipadecrypt
mkdir -p ~/bin
ln -sfn ~/ipadecrypt/src/ipadecrypt ~/bin/ipadecrypt
```

Make sure `~/bin` is in your `PATH`, then verify:

```sh
ipadecrypt --version
```

Refer to [BUILDING.md](BUILDING.md) for helper and release-style build details.

### Jailbreak app

The release includes `.deb` packages for the on-device app and the optional
auto-confirm tweak:

- `com.korboy.ipadecrypt_0.7.3-korboy.8_iphoneos-arm64.deb`
- `com.korboy.ipadecrypt_0.7.3-korboy.8_iphoneos-arm64e.deb` for RootHide
- `com.korboy.ipadecryptautoalert_0.7.3-korboy.2_iphoneos-arm64.deb`
- `com.korboy.ipadecryptautoalert_0.7.3-korboy.2_iphoneos-arm64e.deb` for RootHide

To build and install the rootless app locally:

```sh
./app/build.sh
```

The script builds the app, app-store helper, and daemon, bundles the committed
`internal/device/ipadecrypt-helper-arm64`, installs the package on the phone,
refreshes uicache, and opens `com.korboy.ipadecrypt`.

To build the RootHide app package and upload it to the phone's Downloads
folder:

```sh
./app/build_roothide.sh
```

RootHide output still uses the same app UI and daemon model, but package files
are built with `THEOS_PACKAGE_SCHEME=roothide` and runtime paths are resolved
through the active RootHide jailbreak root.

Pass `--no-install` (or set `IPADECRYPT_NO_INSTALL=1`) to either script to only
build the `.deb` under `app/packages/` without touching the phone.

The app-store helper runs Apple's App Store/FairPlay code under Unicorn, which
has to be statically linked on iOS. Both scripts call
`app/build_unicorn_ios.sh` (macOS + Xcode + cmake), which builds Unicorn 2.1.4
for arm64 iOS once and caches it in `app/.unicorn-ios/`; set `UNICORN_IOS_LIB`
to reuse a prebuilt archive. iOS does not allow writable+executable JIT pages,
so the build patches Unicorn's code buffer to flip between RW and RX with
`mprotect`, and the helper marks itself debugged through Dopamine's
`jbctl proc_set_debugged` the first time it starts Unicorn. That requires a
Dopamine (rootless or RootHide) jailbreak with `jbctl` in its basebin.

## Usage

### First-time setup

```sh
ipadecrypt bootstrap
```

A five-step interactive wizard:

1. **App Store sign-in** - prompts for Apple ID; handles 2FA. Credentials stay local in `~/ipadecrypt/config.json`.
2. **Device connect** - SSH host / user / password; probes iOS version + arch.
3. **Prerequisites** - verifies AppSync and `appinst` are installed.
4. **Helper install** - uploads helper binary and verifies whether it runs.
5. **Auto-confirm tweak** - optionally installs `ipadecryptautoalert`, a SpringBoard tweak that auto-taps the older-version `Download` prompt during **Latest iOS-compatible** installs.
Pass `--skip-login` to skip step 1 when you only need the device steps (for
example decrypting already-installed apps). The first App Store sign-in also
downloads and caches the Apple frameworks the App Store engine emulates
(tens of MB) under your user cache directory (`ipadecrypt/sap`); later runs
reuse the cache.
### Offline decryption (optional)
```sh
ipadecrypt mint                  # one-time, per Apple account
ipadecrypt decrypt --offline <bundle-id|app-store-id|app-store-url|search-term>
```
`mint` connects to the configured device as `root` over SSH (it prompts for
the root password if the saved SSH user is not root; that password is not
saved), extracts the account's 16-byte FairPlay credential, and caches it in
`~/ipadecrypt/credentials/<dsid>.bin`. It skips minting when a credential is
already cached; pass `--remint` to force. The device needs at least one App
Store app owned by the same Apple ID installed.
`--offline` downloads the encrypted IPA and decrypts it on the computer with
that credential. Output goes to `~/ipadecrypt/decrypted/` by default.
### Refresh Apple ID auth
ipadecrypt auth
Refreshes the saved App Store session without running the full bootstrap flow.
Use this when App Store actions fail with an auth-code or expired-token error.
### Choose where decrypted IPAs are kept
ipadecrypt keep
Opens an interactive picker for future CLI decrypt runs. For scripts, pass the
policy directly:
ipadecrypt keep desktop
ipadecrypt keep device
ipadecrypt keep both
`desktop` copies the final IPA to `~/ipadecrypt/decrypted/` and removes the
device copy after a successful transfer. `device` keeps only the device copy.
`both` keeps both copies and is the default.
### Update the CLI
ipadecrypt update
Downloads the latest matching CLI binary from GitHub Releases, verifies it
against `checksums.txt`, backs up the current binary, and replaces it.
Use `ipadecrypt update --check` to check without installing, or
`ipadecrypt update --rollback` to restore the previous binary backup.
A three-step interactive wizard:
1. **App Store sign-in** - Logs into the App Store. Credentials stay local in `~/.ipadecrypt/config.json`. The first sign-in downloads and caches the Apple frameworks the App Store engine emulates (~tens of MB, range-extracted from a pinned macOS image) under `~/.cache/ipadecrypt/`; later runs reuse the cache.
2. **Credential mint** - Connects once to your jailbroken device over root SSH and extracts the reusable account credential, cached under `~/.ipadecrypt/credentials/`.
3. **On-device fallback check** - Verifies the device is ready to decrypt old apps that still ship a 32-bit slice (AppSync Unified + `appinst` present, helper uploads and execs). Best-effort: a missing prerequisite only disables that fallback and is reported, not fatal.
Run it again anytime; it skips minting if a credential is already cached (pass
`--remint` to force). After bootstrap, decrypting modern (arm64) apps is fully
offline - the device is only needed again for the on-device fallback on old
32-bit apps.

### Decrypt an app

```sh
ipadecrypt decrypt <bundle-id|app-store-id|app-store-url|path-to-local-ipa>
```

By default, decrypted IPAs are saved to `~/ipadecrypt/decrypted/` on your
computer and kept on the device under
`/var/mobile/Documents/ipadecrypt/decrypted/` on rootless jailbreaks or
`/rootfs/private/var/mobile/Documents/ipadecrypt/decrypted/` on RootHide.
Run `ipadecrypt keep` to change that retention policy.
When decrypting by bundle ID or App Store search result in a terminal, the
build picker can also open the App Store version table so you can choose a
specific historical version to install and decrypt.

### Download an encrypted IPA
```sh
ipadecrypt download <bundle-id|app-store-id|app-store-url>
```
Downloaded encrypted IPAs are saved to `~/ipadecrypt/<bundleID>_<version>.ipa`
by default. Use `-o/--output` to choose a file or directory,
`--external-version-id` to download a specific historical release, or
`--select-version` to select and download multiple App Store versions.
### Diagnose setup
ipadecrypt doctor
Checks local config, SSH/sudo, jailbreak tooling, helper execution, output
folders, output retention policy, auto-confirm state, and the installed
jailbreak app/daemon.
This decrypts offline. If the app ships a 32-bit slice the offline path can't
handle and a jailbroken device is configured, ipadecrypt automatically falls
back to decrypting it on the device. Useful flags:
- `--on-device` - skip the offline attempt and decrypt on the device directly.
- `--no-on-device` - never touch the device; fail if offline can't fully decrypt.
- `--patch-device-type` - expand `UIDeviceFamily` so an app built for a different device type still installs during the on-device fallback.
- `--no-uninstall` / `--force-uninstall` - control whether the app is removed from the device after an on-device decrypt (default: removed only if this run installed it).

### List versions of an app

```sh
ipadecrypt versions <bundle-id|app-store-id|app-store-url>
```

## Known issues

- [New, unsupported SC_Info format is shipped with some apps, ipadecrypt will fail to decrypt them.](https://github.com/londek/ipadecrypt/issues/34)
- Due to arm64e PPL guards on A12+, some apps such as Apple's are not expected to work.
- `--offline` only decrypts the supported SINF/SUPF license formats, and the credential is per Apple account: you must sign in with (and have minted from) the account that owns the app.
- Only the supported SINF/SUPF license formats decrypt offline; apps shipping a newer, unsupported `SC_Info` format fall back to the on-device path when a device is configured.
- The credential is per Apple account: decrypting an app requires signing in with (and having minted from) the account that owns it.
- Old builds that still ship a 32-bit (armv7) slice can't be decrypted offline (the offline decryptor is arm64-only) - they need the on-device fallback, so a jailbroken device must be configured.

## License

MIT.

## Prior art

- [EthanArbuckle/fairplay-decrypt](https://github.com/EthanArbuckle/fairplay-decrypt) - the offline FairPlay decryptor and credential-minting approach `--offline` is a Go port of.
- [majd/ipatool](https://github.com/majd/ipatool) - the Apple Configurator impersonation the App Store client is based on.
- [34306/TrollDecryptJB](https://github.com/34306/TrollDecryptJB) - `task_for_pid` + `mach_vm_read` from a suspended spawn, entitlement set.
- [akemin-dayo/AppSync](https://github.com/akemin-dayo/AppSync) - installd signature-bypass tweak + `appinst`.
- [JohnCoates/flexdecrypt](https://github.com/JohnCoates/flexdecrypt) - the pre-iOS-16 approach that stopped working and prompted the pivot.

## AI Disclaimer

This project was developed with the assistance of AI tools. While I can't guarantee the accuracy of all AI-generated content, I have overviewed creation process and then reviewed, tested the code to ensure it meets the project's requirements.

<a href="https://star-history.dera.page/#londek/ipadecrypt&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://star-history.dera.page/svg?repos=londek/ipadecrypt&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://star-history.dera.page/svg?repos=londek/ipadecrypt&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://star-history.dera.page/svg?repos=londek/ipadecrypt&type=date&legend=top-left" />
 </picture>
</a>
