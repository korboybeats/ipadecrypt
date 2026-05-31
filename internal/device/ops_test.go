package device

import (
	"strings"
	"testing"
)

func TestClassifyJailbreakRootHide(t *testing.T) {
	for _, sig := range []string{
		"link=/ vjb=1 rh=1 archpkg=iphoneos-arm64e",
		"link=/private/var/containers/Bundle/Application/.jbroot-ABC vjb=1 rh=0",
	} {
		if got := classifyJailbreak(sig); got != "roothide" {
			t.Fatalf("classifyJailbreak(%q) = %q, want roothide", sig, got)
		}
	}
}

func TestClassifyJailbreakDopamine(t *testing.T) {
	sig := "link=/private/preboot/ABC/dopamine-XYZ/procursus vjb=1 rh=0 archpkg=iphoneos-arm64"
	if got := classifyJailbreak(sig); got != "Dopamine" {
		t.Fatalf("classifyJailbreak(%q) = %q, want Dopamine", sig, got)
	}
}

func TestProbeArch(t *testing.T) {
	tests := map[string]string{
		"iphoneos-arm64e": "arm64e",
		"arm64e":          "arm64e",
		"02":              "arm64e",
		"iphoneos-arm64":  "arm64",
		"":                "arm64",
	}
	for in, want := range tests {
		if got := probeArch(in); got != want {
			t.Fatalf("probeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseProbeOutput(t *testing.T) {
	out := `ios=15.0
model=iPhone14,3
arch=iphoneos-arm64e
jb link=/ vjb=1 rh=1 archpkg=iphoneos-arm64e
`
	got := parseProbeOutput(out)
	if got.IOSVersion != "15.0" {
		t.Fatalf("IOSVersion = %q, want 15.0", got.IOSVersion)
	}
	if got.Model != "iPhone14,3" {
		t.Fatalf("Model = %q, want iPhone14,3", got.Model)
	}
	if got.Arch != "arm64e" {
		t.Fatalf("Arch = %q, want arm64e", got.Arch)
	}
	if got.DeviceFamily != 1 {
		t.Fatalf("DeviceFamily = %d, want 1", got.DeviceFamily)
	}
	if got.Jailbreak != "roothide" {
		t.Fatalf("Jailbreak = %q, want roothide", got.Jailbreak)
	}
}

func TestParseInstalledApps(t *testing.T) {
	out := "com.example.one\tOne\t/var/containers/Bundle/Application/A/One.app\nbad line\ncom.example.two\tTwo\t/var/containers/Bundle/Application/B/Two.app\n"
	got := parseInstalledApps(out)
	if len(got) != 2 {
		t.Fatalf("parseInstalledApps returned %d app(s), want 2", len(got))
	}
	if got[0].BundleID != "com.example.one" || got[1].DisplayName != "Two" {
		t.Fatalf("parseInstalledApps returned %#v", got)
	}
}

func TestEnsureBundleExecutablesScriptQuotesBundlePath(t *testing.T) {
	script := ensureBundleExecutablesScript("/var/containers/Bundle/Application/A B/Discord.app")
	if !strings.Contains(script, "bundle='/var/containers/Bundle/Application/A B/Discord.app'") {
		t.Fatalf("script did not quote bundle path correctly:\n%s", script)
	}
	if !strings.Contains(script, `"$bundle"/PlugIns/*.appex`) {
		t.Fatalf("script does not scan appex bundles:\n%s", script)
	}
}

func TestAutoalertDebForJailbreak(t *testing.T) {
	tests := map[string]string{
		"roothide":  "iphoneos-arm64e",
		"Dopamine":  "iphoneos-arm64",
		"rootless?": "iphoneos-arm64",
		"unknown":   "iphoneos-arm64",
	}
	for jailbreak, wantArch := range tests {
		deb, gotArch := autoalertDebForJailbreak(jailbreak)
		if len(deb) == 0 {
			t.Fatalf("autoalertDebForJailbreak(%q) returned empty deb", jailbreak)
		}
		if gotArch != wantArch {
			t.Fatalf("autoalertDebForJailbreak(%q) arch = %q, want %q", jailbreak, gotArch, wantArch)
		}
	}
}
