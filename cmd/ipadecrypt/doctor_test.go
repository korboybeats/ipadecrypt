package main

import (
	"testing"

	"github.com/londek/ipadecrypt/internal/config"
)

func TestDoctorExitCodeFailsOnlyForFailedRequiredChecks(t *testing.T) {
	checks := []doctorCheck{
		{Status: doctorPass, Name: "ssh"},
		{Status: doctorWarn, Name: "autoalert"},
		{Status: doctorSkip, Name: "app daemon"},
	}
	if got := doctorExitCode(checks); got != 0 {
		t.Fatalf("doctorExitCode without failures = %d, want 0", got)
	}

	checks = append(checks, doctorCheck{Status: doctorFail, Name: "appinst"})
	if got := doctorExitCode(checks); got != 1 {
		t.Fatalf("doctorExitCode with failure = %d, want 1", got)
	}
}

func TestDoctorSummaryCountsStatuses(t *testing.T) {
	checks := []doctorCheck{
		{Status: doctorPass},
		{Status: doctorPass},
		{Status: doctorWarn},
		{Status: doctorSkip},
		{Status: doctorFail},
	}

	got := doctorSummary(checks)
	want := "2 ok, 1 warn, 1 fail, 1 skip"
	if got != want {
		t.Fatalf("doctorSummary() = %q, want %q", got, want)
	}
}

func TestParseDoctorKVTrimsLinesAndIgnoresMalformedInput(t *testing.T) {
	got := parseDoctorKV(" appinst = /var/jb/usr/bin/appinst \nmalformed\nzip=/var/jb/usr/bin/zip\nempty=\n")

	if got["appinst"] != "/var/jb/usr/bin/appinst" {
		t.Fatalf("appinst = %q", got["appinst"])
	}
	if got["zip"] != "/var/jb/usr/bin/zip" {
		t.Fatalf("zip = %q", got["zip"])
	}
	if _, ok := got["malformed"]; ok {
		t.Fatalf("malformed line should be ignored")
	}
	if got["empty"] != "" {
		t.Fatalf("empty = %q, want empty string", got["empty"])
	}
}

func TestDoctorLocalChecksIncludesSelfUpdate(t *testing.T) {
	paths, err := config.NewPaths(t.TempDir())
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	cfg := config.New(paths.ConfigPath())

	checks := doctorLocalChecks(cfg, paths, "v0.6.2-korboy.1")
	for _, check := range checks {
		if check.Name == "CLI self-update" {
			if check.Status != doctorPass {
				t.Fatalf("CLI self-update status = %s, want %s", check.Status, doctorPass)
			}
			return
		}
	}
	t.Fatal("missing CLI self-update check")
}
