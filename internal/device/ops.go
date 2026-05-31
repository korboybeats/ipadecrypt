package device

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"howett.net/plist"
)

//go:embed ipadecrypt-helper-arm64
var helperArm64 []byte

//go:embed appdl-arm64
var appdlArm64 []byte

//go:embed ipadecryptautoalert.deb
var autoalertRootlessDeb []byte

//go:embed ipadecryptautoalert-roothide.deb
var autoalertRoothideDeb []byte

const autoalertPackage = "com.korboy.ipadecryptautoalert"

// SSH non-interactive shells on iOS often have a trimmed PATH that omits
// sysctl and rootless tools, so this script tries absolute paths before
// falling back. `uname -m` always reports arm64 on iOS; RootHide's dpkg
// architecture is a better arm64e signal than /sbin/launchd on remapped
// filesystems.
const deviceProbeScript = `IOS=$(sw_vers -productVersion 2>/dev/null || /usr/libexec/PlistBuddy -c 'Print :ProductVersion' /System/Library/CoreServices/SystemVersion.plist 2>/dev/null || true)
MODEL=$(sysctl -n hw.machine 2>/dev/null || /usr/sbin/sysctl -n hw.machine 2>/dev/null || /var/jb/usr/sbin/sysctl -n hw.machine 2>/dev/null || (sysctl hw.machine 2>/dev/null | sed 's/^hw.machine: *//') || true)
PKGARCH=$(dpkg --print-architecture 2>/dev/null || /var/jb/usr/bin/dpkg --print-architecture 2>/dev/null || true)
ARCH="$PKGARCH"
if [ -z "$ARCH" ]; then ARCH=$(od -An -tx1 -j8 -N1 /sbin/launchd 2>/dev/null | tr -d ' \n'); fi
VJB=0; [ -e /var/jb ] && VJB=1
LINK=$(readlink /var/jb 2>/dev/null)
RH=0
ls -d /var/containers/Bundle/Application/.jbroot-* /private/var/containers/Bundle/Application/.jbroot-* /rootfs/var/containers/Bundle/Application/.jbroot-* >/dev/null 2>&1 && RH=1
[ "$LINK" = "/" ] && [ "$PKGARCH" = "iphoneos-arm64e" ] && RH=1
printf 'ios=%s\n' "$IOS"
printf 'model=%s\n' "$MODEL"
printf 'arch=%s\n' "$ARCH"
printf 'jb link=%s vjb=%s rh=%s archpkg=%s\n' "$LINK" "$VJB" "$RH" "$PKGARCH"`

type ProbeResult struct {
	IOSVersion string
	Arch       string // "arm64" or "arm64e"
	Model      string // "iPhone10,2", "iPad7,3", …
	// DeviceFamily mirrors UIDeviceFamily values from Info.plist:
	// 1 = iPhone/iPod, 2 = iPad. 0 if unknown.
	DeviceFamily int
	// Jailbreak is the detected jailbreak: "roothide", "Dopamine",
	// "palera1n", "checkra1n", "rootless?" or "unknown".
	Jailbreak string
}

// classifyJailbreak identifies the active jailbreak from live filesystem
// signals. /var/jb is the rootless prefix every modern jailbreak mounts at
// boot, so its target reflects the bootstrap currently running. RootHide can
// remap that view, so Probe also passes explicit RootHide markers from the app
// container namespace and dpkg's active package architecture.
//
// Verified on iPhone10,5 / A11 / Dopamine: target is
// /private/preboot/<hash>/dopamine-<rand>/procursus. RootHide Dopamine also
// uses a .jbroot-<hex> scheme. palera1n's rootless bootstrap sits under
// a jb-<rand> dir; checkra1n is rootful (no /var/jb -> preboot link). The
// "dopamine" token is empirically confirmed; the palera1n/checkra1n schemes
// are from their known layouts (no such hardware here to confirm).
func classifyJailbreak(sig string) string {
	f := map[string]string{}

	for _, tok := range strings.Fields(sig) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			f[k] = v
		}
	}

	link := strings.ToLower(f["link"])
	switch {
	case f["rh"] == "1" || strings.Contains(link, ".jbroot"):
		return "roothide"
	case strings.Contains(link, "dopamine"):
		return "Dopamine"
	case strings.Contains(link, "palera1n") || strings.Contains(link, "/jb-"):
		return "palera1n"
	case f["vjb"] == "1":
		return "rootless?"
	default:
		return "unknown"
	}
}

func probeArch(raw string) string {
	switch strings.TrimSpace(raw) {
	case "iphoneos-arm64e", "arm64e", "02":
		return "arm64e"
	default:
		return "arm64"
	}
}

func deviceFamilyFromModel(model string) int {
	switch {
	case strings.HasPrefix(model, "iPhone"), strings.HasPrefix(model, "iPod"):
		return 1
	case strings.HasPrefix(model, "iPad"):
		return 2
	default:
		return 0
	}
}

func (c *Client) Probe() (ProbeResult, error) {
	out, _, code, err := c.Run(deviceProbeScript)
	if err != nil || code != 0 {
		return ProbeResult{}, fmt.Errorf("probe (exit %d): %w", code, err)
	}

	return parseProbeOutput(out), nil
}

func parseProbeOutput(out string) ProbeResult {
	var r ProbeResult
	r.Arch = "arm64"
	r.Jailbreak = "unknown"

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		if sig, ok := strings.CutPrefix(s, "jb "); ok {
			r.Jailbreak = classifyJailbreak(sig)
			continue
		}
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			continue
		}
		switch key {
		case "ios":
			r.IOSVersion = strings.TrimSpace(value)
		case "model":
			r.Model = strings.TrimSpace(value)
		case "arch":
			r.Arch = probeArch(value)
		}
	}

	r.DeviceFamily = deviceFamilyFromModel(r.Model)
	return r
}

func (c *Client) LocateAppinst() (string, error) {
	out, _, _, err := c.Run("command -v appinst 2>/dev/null || true")
	if err != nil {
		return "", fmt.Errorf("locate appinst: %w", err)
	}

	if p := strings.TrimSpace(out); p != "" {
		return p, nil
	}

	for _, candidate := range []string{
		"/usr/local/bin/appinst",
		"/var/jb/usr/bin/appinst",
		"/var/jb/usr/local/bin/appinst",
	} {
		if c.Exists(candidate) {
			return candidate, nil
		}
	}

	return "", nil
}

func (c *Client) LocateAppSync() (string, error) {
	candidates := []string{
		"/var/jb/Library/MobileSubstrate/DynamicLibraries/AppSyncUnified-installd.dylib",
		"/Library/MobileSubstrate/DynamicLibraries/AppSyncUnified-installd.dylib",
		"/var/jb/Library/MobileSubstrate/DynamicLibraries/AppSyncUnified.dylib",
		"/Library/MobileSubstrate/DynamicLibraries/AppSyncUnified.dylib",
	}

	for _, p := range candidates {
		if c.Exists(p) {
			return p, nil
		}
	}

	out, _, _, err := c.Run(
		"ls /Library/MobileSubstrate/DynamicLibraries/AppSyncUnified*.dylib " +
			"/var/jb/Library/MobileSubstrate/DynamicLibraries/AppSyncUnified*.dylib " +
			"2>/dev/null | head -1")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

func (c *Client) Install(appinstPath, ipaRemote string) error {
	out, errOut, code, err := c.RunSudo(fmt.Sprintf("%s %q", appinstPath, ipaRemote))
	if err != nil {
		return fmt.Errorf("appinst: %w", err)
	}

	if code != 0 {
		return fmt.Errorf("appinst exit %d:\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	return nil
}

// Uninstall removes an installed app. Two steps wrapped in a single
// `sudo sh -c '…'` so both commands run as root (sudo only elevates the
// first command in a `cmd1; cmd2` chain, otherwise rm runs as mobile and
// hits the _installd-owned files with EACCES). Order matters:
//
//  1. uicache -u <bundlePath>  - unregister from LSApplicationWorkspace
//     so SpringBoard drops the icon and the LS registry forgets the
//     bundle.
//  2. rm -rf <UUID-dir>        - remove the bundle dir under
//     /var/containers/Bundle/Application/.
//
// The data container at /var/mobile/Containers/Data/Application/<other-UUID>/
// is left behind  finding it would require scanning every container's
// metadata plist for the bundle id, and the next install spins a fresh
// one regardless.
func (c *Client) Uninstall(bundlePath string) error {
	const bundleRoot = "/var/containers/Bundle/Application/"
	if !strings.HasPrefix(bundlePath, bundleRoot) {
		return fmt.Errorf("refuse to uninstall: suspicious bundle path %q", bundlePath)
	}

	uuidDir := path.Dir(bundlePath)

	cmd := fmt.Sprintf("sh -c %s",
		shellQuote(fmt.Sprintf("uicache -u %s && rm -rf %s",
			shellQuote(bundlePath), shellQuote(uuidDir))))

	out, errOut, code, err := c.RunSudo(cmd)
	if err != nil {
		return fmt.Errorf("uninstall: %w", err)
	}

	if code != 0 {
		return fmt.Errorf("uninstall exit %d:\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	return nil
}

func (c *Client) EnsureHelper() (string, error) {
	c.CleanupLegacyRemoteRoot()

	sum := sha256.Sum256(helperArm64)
	remote := helperRemotePath(hex.EncodeToString(sum[:])[:12])

	if c.Exists(remote) {
		return remote, nil
	}

	if err := c.Upload(bytes.NewReader(helperArm64), remote, 0o755); err != nil {
		return "", fmt.Errorf("upload helper: %w", err)
	}

	return remote, nil
}

func helperRemotePath(sumPrefix string) string {
	return path.Join(RemoteRoot, "helpers", fmt.Sprintf("ipadecrypt-helper-arm64-%s.bin", sumPrefix))
}

func (c *Client) CleanupLegacyRemoteRoot() {
	_, _, _, _ = c.RunSudo(fmt.Sprintf("rm -rf %q", LegacyRemoteRoot))
}

// IsAutoalertInstalled checks if the SpringBoard auto-confirm tweak is
// installed via dpkg.
func (c *Client) IsAutoalertInstalled(jailbreak string) bool {
	_, expectedArch := autoalertDebForJailbreak(jailbreak)
	out, _, code, _ := c.RunSudo(fmt.Sprintf("dpkg -s %s 2>/dev/null | awk -F': ' '/^Status:/{status=$2} /^Architecture:/{arch=$2} END{if (status==\"install ok installed\") print arch}'", autoalertPackage))
	return code == 0 && strings.TrimSpace(out) == expectedArch
}

// EnsureAutoalert installs the SpringBoard auto-confirm tweak via dpkg.
// Caller is responsible for respringing SpringBoard afterward.
func (c *Client) EnsureAutoalert(jailbreak string) error {
	if c.IsAutoalertInstalled(jailbreak) {
		return nil
	}
	deb, _ := autoalertDebForJailbreak(jailbreak)
	remote := path.Join(RemoteRoot, "tweaks", autoalertDebName(jailbreak))
	if err := c.Upload(bytes.NewReader(deb), remote, 0o644); err != nil {
		return fmt.Errorf("upload tweak deb: %w", err)
	}
	_, errOut, code, err := c.RunSudo(fmt.Sprintf("dpkg -i %q", remote))
	if err != nil {
		return fmt.Errorf("dpkg -i: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("dpkg -i exit %d: %s", code, strings.TrimSpace(errOut))
	}
	return nil
}

func autoalertDebForJailbreak(jailbreak string) ([]byte, string) {
	if jailbreak == "roothide" {
		return autoalertRoothideDeb, "iphoneos-arm64e"
	}
	return autoalertRootlessDeb, "iphoneos-arm64"
}

func autoalertDebName(jailbreak string) string {
	if jailbreak == "roothide" {
		return "ipadecryptautoalert-roothide.deb"
	}
	return "ipadecryptautoalert-rootless.deb"
}

// Respring kills SpringBoard so it relaunches.
func (c *Client) Respring() error {
	_, errOut, code, err := c.RunSudo("killall SpringBoard")
	if err != nil {
		return fmt.Errorf("killall: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("killall exit %d: %s", code, strings.TrimSpace(errOut))
	}
	return nil
}

// EnsureAppdl uploads the StoreKit download helper if not already present and
// adds its CDHash to Dopamine's trust cache so AMFI honors its entitlements.
// Returns the remote path. Trust cache add is idempotent and harmless on
// jailbreaks where it's not needed.
func (c *Client) EnsureAppdl() (string, error) {
	sum := sha256.Sum256(appdlArm64)
	remote := path.Join(RemoteRoot, "helpers",
		fmt.Sprintf("ipadecrypt-appdl-arm64-%s.bin", hex.EncodeToString(sum[:])[:12]))

	if !c.Exists(remote) {
		if err := c.Upload(bytes.NewReader(appdlArm64), remote, 0o755); err != nil {
			return "", fmt.Errorf("upload appdl: %w", err)
		}
	}

	// Best-effort: try to add to Dopamine's trust cache. Ignore errors
	// (jailbreak may not be Dopamine, or jbctl may already have it).
	cdhash, err := c.appdlCDHash(remote)
	if err == nil && cdhash != "" {
		_, _, _, _ = c.RunSudo(fmt.Sprintf("/var/jb/basebin/jbctl trustcache add %s 2>/dev/null", cdhash))
	}

	return remote, nil
}

func (c *Client) appdlCDHash(remote string) (string, error) {
	out, _, _, err := c.RunSudo(fmt.Sprintf("ldid -h %q 2>/dev/null | grep '^CDHash=' | cut -d= -f2", remote))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RunAppdl runs the StoreKit download helper for a bundle ID or app ID.
// stdout/stderr is streamed via onLine. Returns the helper's exit code.
func (c *Client) RunAppdl(appdlPath, target string, onLine func(line string)) (int, error) {
	cmd := fmt.Sprintf("%s %q", appdlPath, target)
	stdout, stderr, code, err := c.RunSudo(cmd)
	if onLine != nil {
		for _, line := range strings.Split(strings.TrimSpace(stdout+"\n"+stderr), "\n") {
			if line == "" {
				continue
			}
			onLine(line)
		}
	}
	return code, err
}

// HashFile computes the sha256 of a path on-device. Installed bundles under
// /var/containers are readable only by root + _installd, hence sudo. Relies
// on a `shasum` binary being on PATH (procursus/dopamine/palera1n all ship
// it at /var/jb/usr/bin/shasum).
func (c *Client) HashFile(target string) (string, error) {
	// procursus (Dopamine + palera1n) ships sha256sum (from coreutils) and
	// shasum (perl). Try both. Output is `<hex>  <path>`; cut first field.
	cmd := fmt.Sprintf(
		"sh -c '"+
			"for p in sha256sum /var/jb/usr/bin/sha256sum /usr/bin/sha256sum "+
			"shasum /var/jb/usr/bin/shasum /usr/bin/shasum; do "+
			"  if command -v \"$p\" >/dev/null 2>&1; then "+
			"    case \"$p\" in "+
			"      *shasum) \"$p\" -a 256 %[1]q | cut -d\" \" -f1; exit 0;; "+
			"      *) \"$p\" %[1]q | cut -d\" \" -f1; exit 0;; "+
			"    esac; "+
			"  fi; "+
			"done; exit 127"+
			"'",
		target)

	out, errOut, code, err := c.RunSudo(cmd)
	if err != nil {
		return "", fmt.Errorf("shasum: %w", err)
	}

	if code != 0 {
		return "", fmt.Errorf("shasum exit %d: %s", code, strings.TrimSpace(errOut))
	}

	return strings.TrimSpace(out), nil
}

// InstalledVersion reads CFBundleShortVersionString (falling back to
// CFBundleVersion) from an installed app bundle's Info.plist.
func (c *Client) InstalledVersion(bundlePath string) (string, error) {
	infoPath := path.Join(bundlePath, "Info.plist")

	out, errOut, code, err := c.RunSudo(fmt.Sprintf("cat %q", infoPath))
	if err != nil {
		return "", fmt.Errorf("read installed version: %w", err)
	}

	if code != 0 {
		return "", fmt.Errorf("read installed version exit %d: %s", code, strings.TrimSpace(errOut))
	}

	var info map[string]any
	if _, err := plist.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("parse installed Info.plist: %w", err)
	}

	if version, _ := info["CFBundleShortVersionString"].(string); version != "" {
		return version, nil
	}

	if version, _ := info["CFBundleVersion"].(string); version != "" {
		return version, nil
	}

	if version, ok := info["CFBundleShortVersionString"]; ok {
		return strings.TrimSpace(fmt.Sprintf("%v", version)), nil
	}

	if version, ok := info["CFBundleVersion"]; ok {
		return strings.TrimSpace(fmt.Sprintf("%v", version)), nil
	}

	return "", errors.New("installed version not found")
}

// InstalledApp describes one installed app.
type InstalledApp struct {
	BundleID    string
	DisplayName string // CFBundleDisplayName, falling back to CFBundleName
	Path        string // /var/containers/Bundle/Application/<UUID>/<Name>.app
}

// SearchInstalledApps returns installed apps whose Info.plist contains term.
// It prefilters matching plists before parsing, so common fuzzy searches do
// not enumerate every installed app.
func (c *Client) SearchInstalledApps(term string) ([]InstalledApp, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}

	out, _, code, err := c.RunSudo("sh -c " + shellQuote(installedAppSearchScript(term)))
	if err != nil {
		return nil, fmt.Errorf("search installed: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("search installed exit %d", code)
	}

	return parseInstalledApps(out), nil
}

func installedAppSearchScript(term string) string {
	return `plutil_bin=""
for p in /usr/bin/plutil /var/jb/usr/bin/plutil; do
    if [ -x "$p" ]; then plutil_bin="$p"; break; fi
done
[ -n "$plutil_bin" ] || exit 127
plget() {
    "$plutil_bin" -key "$1" "$2" 2>/dev/null | sed -n '1p'
}
grep -RilaF -- ` + shellQuote(term) + ` /var/containers/Bundle/Application/*/*.app/Info.plist 2>/dev/null | while IFS= read -r info; do
    [ -r "$info" ] || continue
    app=${info%/Info.plist}
    bid=$(plget CFBundleIdentifier "$info")
    [ -n "$bid" ] || continue
    name=$(plget CFBundleDisplayName "$info")
    [ -n "$name" ] || name=$(plget CFBundleName "$info")
    printf '%s\t%s\t%s\n' "$bid" "$name" "$app"
done`
}

func parseInstalledApps(out string) []InstalledApp {
	var apps []InstalledApp
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		apps = append(apps, InstalledApp{BundleID: parts[0], DisplayName: parts[1], Path: parts[2]})
	}
	return apps
}

// EnsureBundleExecutables sets +x on the main executable for the app, appex,
// and framework bundles the helper may need to spawn or read. Some App Store
// installs leave extension executables mode 0644; doing this over SSH as root
// is more reliable on RootHide than asking the helper process to chmod later.
func (c *Client) EnsureBundleExecutables(bundlePath string) error {
	out, errOut, code, err := c.RunSudo("sh -c " + shellQuote(ensureBundleExecutablesScript(bundlePath)))
	if err != nil {
		return fmt.Errorf("prepare executable modes: %w", err)
	}
	if code != 0 {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		return fmt.Errorf("prepare executable modes exit %d: %s", code, msg)
	}
	return nil
}

func ensureBundleExecutablesScript(bundlePath string) string {
	return `bundle=` + shellQuote(bundlePath) + `
plutil_bin=""
for p in /usr/bin/plutil /var/jb/usr/bin/plutil; do
    if [ -x "$p" ]; then plutil_bin="$p"; break; fi
done
bundle_executable() {
    b="$1"
    if [ -n "$plutil_bin" ] && [ -r "$b/Info.plist" ]; then
        exe=$("$plutil_bin" -key CFBundleExecutable "$b/Info.plist" 2>/dev/null | sed -n '1p')
        if [ -n "$exe" ]; then printf '%s\n' "$exe"; return 0; fi
    fi
    base=${b##*/}
    printf '%s\n' "${base%.*}"
}
chmod_bundle_exec() {
    b="$1"
    [ -d "$b" ] || return 0
    exe=$(bundle_executable "$b")
    [ -n "$exe" ] || return 0
    target="$b/$exe"
    [ -f "$target" ] || return 0
    chmod a+x "$target"
}
chmod_bundle_exec "$bundle"
for b in "$bundle"/Frameworks/*.framework "$bundle"/PlugIns/*.appex "$bundle"/Extensions/*.appex; do
    chmod_bundle_exec "$b"
done`
}

// FindInstalledByBundleID returns the .app path and canonical CFBundleIdentifier
// matching bundleID case-insensitively. grep is a prefilter; each hit is parsed
// to confirm (binary plists embed bundle ids as URL-scheme substrings).
func (c *Client) FindInstalledByBundleID(bundleID string) (string, string, error) {
	cmd := "grep -laFi " + shellQuote(bundleID) +
		" /var/containers/Bundle/Application/*/*.app/Info.plist 2>/dev/null"

	out, errOut, code, err := c.RunSudo(cmd)
	if err != nil {
		return "", "", err
	}

	if code != 0 && code != 1 {
		return "", "", fmt.Errorf("find-by-bundle-id exit %d: %s", code, strings.TrimSpace(errOut))
	}

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		infoPath := strings.TrimSpace(line)
		if infoPath == "" {
			continue
		}

		bundlePath := strings.TrimSuffix(infoPath, "/Info.plist")

		got, err := c.bundleIdentifierAt(infoPath)
		if err != nil {
			continue
		}

		if strings.EqualFold(got, bundleID) {
			return bundlePath, got, nil
		}
	}

	return "", "", nil
}

func (c *Client) bundleIdentifierAt(infoPlistPath string) (string, error) {
	out, errOut, code, err := c.RunSudo(fmt.Sprintf("cat %q", infoPlistPath))
	if err != nil {
		return "", fmt.Errorf("read Info.plist: %w", err)
	}

	if code != 0 {
		return "", fmt.Errorf("read Info.plist exit %d: %s", code, strings.TrimSpace(errOut))
	}

	var info map[string]any
	if _, err := plist.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("parse Info.plist: %w", err)
	}

	id, _ := info["CFBundleIdentifier"].(string)
	return id, nil
}

// VerifyHelper is a best-effort sanity: invoke the helper with no args; it
// should exit 2 with a usage string we can recognize. Catches common issues
// (binary not executable, sudo denied, missing codesign).
func (c *Client) VerifyHelper(helperPath string) error {
	cmd := fmt.Sprintf("%s 2>&1 | head -1", helperPath)

	out, _, _, err := c.RunSudo(cmd)
	if err != nil {
		return fmt.Errorf("verify helper: %w", err)
	}

	if !strings.Contains(out, "usage:") {
		return fmt.Errorf("helper didn't respond with usage (got %q)", strings.TrimSpace(out))
	}

	return nil
}

type EventHandler func(Event)

// FrameHandler is invoked once per Mach-O record the helper streams in
// --execs-only mode. path is the IPA-relative bundle path
// ("Payload/AppName.app/Frameworks/X.framework/X"). r yields exactly size
// bytes; the handler must read them fully before returning, or the next
// frame will deserialize garbage.
type FrameHandler func(path string, size int64, r io.Reader) error

// RunHelper spawns the on-device helper for a bundle and streams the
// decrypted IPA bytes into ipaW. bundleID goes to the SpringBoard SBS SPI
// (only accepted for the main app; empty string skips the main-app pass
// and just decrypts PlugIns/*.appex + Extensions/*.appex). When skipAppex
// is true the helper passes --skip-appex, leaving extensions encrypted.
//
// In this mode the helper writes IPA bytes to stdout and event lines to
// stderr; we tee stderr through the event splitter while ipaW receives
// the binary stream - no on-device output file, no sftp pull.
func (c *Client) RunHelper(helperPath, bundleID, bundlePath string,
	verbose, skipAppex bool, onEvent EventHandler, ipaW io.Writer) (int, error) {
	if err := c.EnsureBundleExecutables(bundlePath); err != nil {
		return 1, err
	}

	gflag := ""
	if verbose {
		gflag = "-v "
	}

	subflag := ""
	if skipAppex {
		subflag = "--skip-appex "
	}

	cmd := fmt.Sprintf("%s %sdecrypt %s%q %q -",
		helperPath, gflag, subflag, bundleID, bundlePath)

	splitter := newEventSplitter(onEvent, io.Discard)
	defer splitter.Close()

	_, _, code, err := c.RunSudoStream(cmd, ipaW, splitter)

	return code, err
}

// RunHelperExecs runs the helper in --execs-only mode: the helper streams
// framed Mach-O records on stdout, events on stderr. onFrame is called
// once per record. Use this when the host has the source IPA and will
// assemble the output by substituting the helper-provided execs.
func (c *Client) RunHelperExecs(helperPath, bundleID, bundlePath string,
	verbose, skipAppex bool, onEvent EventHandler, onFrame FrameHandler) (int, error) {
	if err := c.EnsureBundleExecutables(bundlePath); err != nil {
		return 1, err
	}

	gflag := ""
	if verbose {
		gflag = "-v "
	}

	subflag := "--execs-only "
	if skipAppex {
		subflag += "--skip-appex "
	}

	cmd := fmt.Sprintf("%s %sdecrypt %s%q %q",
		helperPath, gflag, subflag, bundleID, bundlePath)

	splitter := newEventSplitter(onEvent, io.Discard)
	defer splitter.Close()

	// Tee helper stdout into a pipe we read frames from. The SSH session
	// blocks in a goroutine; we read frames from the main goroutine and
	// signal completion via done.
	pr, pw := io.Pipe()

	var (
		code   int
		runErr error
		done   = make(chan struct{})
	)

	go func() {
		_, _, code, runErr = c.RunSudoStream(cmd, pw, splitter)
		pw.Close()
		close(done)
	}()

	parseErr := parseFrames(pr, onFrame)

	// Drain any remaining stdout so the SSH session can complete.
	io.Copy(io.Discard, pr)
	<-done

	if runErr != nil {
		return code, runErr
	}

	if parseErr != nil {
		return code, parseErr
	}

	return code, nil
}

// parseFrames reads [u32be plen][plen-byte path][u64be size][size bytes]
// records until EOF, invoking onFrame for each.
func parseFrames(r io.Reader, onFrame FrameHandler) error {
	br := bufio.NewReaderSize(r, 64*1024)

	for {
		var hdr [4]byte
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("read frame plen: %w", err)
		}

		plen := binary.BigEndian.Uint32(hdr[:])
		if plen == 0 || plen > 4096 {
			return fmt.Errorf("invalid frame path length: %d", plen)
		}

		pathBuf := make([]byte, plen)
		if _, err := io.ReadFull(br, pathBuf); err != nil {
			return fmt.Errorf("read frame path: %w", err)
		}

		var szHdr [8]byte
		if _, err := io.ReadFull(br, szHdr[:]); err != nil {
			return fmt.Errorf("read frame size: %w", err)
		}

		size := int64(binary.BigEndian.Uint64(szHdr[:]))
		if size < 0 {
			return fmt.Errorf("negative frame size: %d", size)
		}

		lr := io.LimitReader(br, size)
		if err := onFrame(string(pathBuf), size, lr); err != nil {
			return err
		}

		// Drain leftovers in case the handler didn't read everything.
		if _, err := io.Copy(io.Discard, lr); err != nil {
			return fmt.Errorf("drain frame body: %w", err)
		}
	}
}

type eventSplitter struct {
	pw *io.PipeWriter
}

func (s *eventSplitter) Write(p []byte) (int, error) { return s.pw.Write(p) }
func (s *eventSplitter) Close() error                { return s.pw.Close() }

func newEventSplitter(onEvent EventHandler, humanFallback io.Writer) *eventSplitter {
	pr, pw := io.Pipe()

	go func() {
		defer pr.Close()

		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 1<<16), 1<<20)

		for sc.Scan() {
			line := sc.Text()
			if ev, ok := ParseEvent(line); ok {
				if onEvent != nil {
					onEvent(ev)
				}

				continue
			}

			if humanFallback != nil {
				fmt.Fprintln(humanFallback, line)
			}
		}
	}()

	return &eventSplitter{pw: pw}
}
