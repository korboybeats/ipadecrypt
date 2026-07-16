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
- **Short command flags.** `-d` (decrypt), `-b` (bootstrap), `-v` (versions), `-dl` (download), `-a`/`auth` (refresh Apple ID auth), `-k` (keep policy), `-u` (update).

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

- `com.korboy.ipadecrypt_0.7.3-korboy.1_iphoneos-arm64.deb`
- `com.korboy.ipadecrypt_0.7.3-korboy.1_iphoneos-arm64e.deb` for RootHide
- `com.korboy.ipadecryptautoalert_0.7.3-korboy.1_iphoneos-arm64.deb`
- `com.korboy.ipadecryptautoalert_0.7.3-korboy.1_iphoneos-arm64e.deb` for RootHide

To build and install the rootless app locally:

```sh
./app/build.sh
```

The script builds the app, app-store helper, and daemon, installs the package
on the phone, refreshes uicache, and opens `com.korboy.ipadecrypt`.

To build the RootHide app package and upload it to the phone's Downloads
folder:

```sh
./app/build_roothide.sh
```

RootHide output still uses the same app UI and daemon model, but package files
are built with `THEOS_PACKAGE_SCHEME=roothide` and runtime paths are resolved
through the active RootHide jailbreak root.

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

### Refresh Apple ID auth

```sh
ipadecrypt auth
```

Refreshes the saved App Store session without running the full bootstrap flow.
Use this when App Store actions fail with an auth-code or expired-token error.

### Choose where decrypted IPAs are kept

```sh
ipadecrypt keep
```

Opens an interactive picker for future CLI decrypt runs. For scripts, pass the
policy directly:

```sh
ipadecrypt keep desktop
ipadecrypt keep device
ipadecrypt keep both
```

`desktop` copies the final IPA to `~/ipadecrypt/decrypted/` and removes the
device copy after a successful transfer. `device` keeps only the device copy.
`both` keeps both copies and is the default.

### Update the CLI

```sh
ipadecrypt update
```

Downloads the latest matching CLI binary from GitHub Releases, verifies it
against `checksums.txt`, backs up the current binary, and replaces it.

Use `ipadecrypt update --check` to check without installing, or
`ipadecrypt update --rollback` to restore the previous binary backup.

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

```sh
ipadecrypt doctor
```

Checks local config, SSH/sudo, jailbreak tooling, helper execution, output
folders, output retention policy, auto-confirm state, and the installed
jailbreak app/daemon.

### List versions of an app

```sh
ipadecrypt versions <bundle-id|app-store-id|app-store-url>
```

## Known issues

- [New, unsupported SC_Info format is shipped with some apps, ipadecrypt will fail to decrypt them.](https://github.com/londek/ipadecrypt/issues/34)
- Due to arm64e PPL guards on A12+, some apps such as Apple's are not expected to work.

## License

MIT.

## Prior art

- [majd/ipatool](https://github.com/majd/ipatool) - the Apple Configurator impersonation the App Store client is based on.
- [34306/TrollDecryptJB](https://github.com/34306/TrollDecryptJB) - `task_for_pid` + `mach_vm_read` from a suspended spawn, entitlement set.
- [akemin-dayo/AppSync](https://github.com/akemin-dayo/AppSync) - installd signature-bypass tweak + `appinst`.
- [JohnCoates/flexdecrypt](https://github.com/JohnCoates/flexdecrypt) - the pre-iOS-16 approach that stopped working and prompted the pivot.

## AI Disclaimer

This project was developed with the assistance of AI tools. While I can't guarantee the accuracy of all AI-generated content, I have overviewed creation process and then reviewed, tested the code to ensure it meets the project's requirements.

<a href="https://www.star-history.com/?repos=londek%2Fipadecrypt&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=londek/ipadecrypt&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=londek/ipadecrypt&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=londek/ipadecrypt&type=date&legend=top-left" />
 </picture>
</a>
