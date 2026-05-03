package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/device"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/spf13/cobra"
)

type doctorStatus string

const (
	doctorPass doctorStatus = "ok"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
	doctorSkip doctorStatus = "skip"
)

type doctorCheck struct {
	Status doctorStatus
	Name   string
	Detail string
	Hint   string
}

type doctorSink struct {
	Start func(string)
	Check func(doctorCheck)
}

type doctorRun struct {
	checks []doctorCheck
	sink   doctorSink
}

func doctorHandler(cmdCtx *cobra.Command, args []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("load config: %v", err)
		os.Exit(1)
	}

	tui.Header("Doctor")
	checks := runDoctor(cfg, paths, Version, newDoctorConsoleSink())
	printDoctorSummary(checks)

	if code := doctorExitCode(checks); code != 0 {
		os.Exit(code)
	}
}

func newDoctorConsoleSink() doctorSink {
	var live *tui.Live
	stop := func() {
		if live != nil {
			live.Stop()
			live = nil
		}
	}

	return doctorSink{
		Start: func(name string) {
			stop()
			live = tui.NewLive()
			live.Spin("checking %s", name)
		},
		Check: func(c doctorCheck) {
			stop()
			printDoctorCheck(c)
		},
	}
}

func runDoctor(cfg *config.Config, paths *config.Paths, version string, sink doctorSink) []doctorCheck {
	r := &doctorRun{sink: sink}

	r.addAll(doctorLocalChecks(cfg, paths, version))
	if !doctorDeviceConfigReady(cfg) {
		r.add(doctorCheck{
			Status: doctorSkip,
			Name:   "remote checks",
			Detail: "device connection is not configured",
			Hint:   "run ipadecrypt bootstrap",
		})
		return r.checks
	}

	r.start("SSH connection")
	dev, err := device.Connect(context.Background(), cfg.Device)
	if err != nil {
		r.add(doctorCheck{
			Status: doctorFail,
			Name:   "ssh",
			Detail: err.Error(),
			Hint:   "check host, port, credentials, and OpenSSH on the phone",
		})
		r.add(doctorCheck{
			Status: doctorSkip,
			Name:   "remote checks",
			Detail: "SSH connection failed",
		})
		return r.checks
	}
	defer dev.Close()

	r.add(doctorCheck{
		Status: doctorPass,
		Name:   "ssh",
		Detail: fmt.Sprintf("connected to %s", dev.Host()),
	})
	r.remoteChecks(dev)
	return r.checks
}

func (r *doctorRun) start(name string) {
	if r.sink.Start != nil {
		r.sink.Start(name)
	}
}

func (r *doctorRun) add(c doctorCheck) {
	r.checks = append(r.checks, c)
	if r.sink.Check != nil {
		r.sink.Check(c)
	}
}

func (r *doctorRun) addAll(checks []doctorCheck) {
	for _, c := range checks {
		r.add(c)
	}
}

func doctorLocalChecks(cfg *config.Config, paths *config.Paths, version string) []doctorCheck {
	checks := []doctorCheck{{
		Status: doctorPass,
		Name:   "workspace",
		Detail: paths.Root,
	}}

	if _, err := os.Stat(paths.ConfigPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			checks = append(checks, doctorCheck{
				Status: doctorFail,
				Name:   "config",
				Detail: "config.json not found",
				Hint:   "run ipadecrypt bootstrap",
			})
		} else {
			checks = append(checks, doctorCheck{
				Status: doctorFail,
				Name:   "config",
				Detail: err.Error(),
			})
		}
	} else {
		checks = append(checks, doctorCheck{
			Status: doctorPass,
			Name:   "config",
			Detail: paths.ConfigPath(),
		})
	}

	outDir := filepath.Join(paths.Root, "decrypted")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		checks = append(checks, doctorCheck{
			Status: doctorFail,
			Name:   "local output",
			Detail: err.Error(),
		})
	} else {
		checks = append(checks, doctorCheck{
			Status: doctorPass,
			Name:   "local output",
			Detail: outDir,
		})
	}

	if cfg.Apple.Account != nil {
		checks = append(checks, doctorCheck{
			Status: doctorPass,
			Name:   "App Store session",
			Detail: "cached account present",
		})
	} else {
		checks = append(checks, doctorCheck{
			Status: doctorWarn,
			Name:   "App Store session",
			Detail: "no cached account",
			Hint:   "run ipadecrypt bootstrap before App Store downloads",
		})
	}

	if version == "" || version == "dev" {
		checks = append(checks, doctorCheck{
			Status: doctorWarn,
			Name:   "CLI version",
			Detail: "development build",
		})
	} else {
		checks = append(checks, doctorCheck{
			Status: doctorPass,
			Name:   "CLI version",
			Detail: version,
		})
	}

	return checks
}

func doctorDeviceConfigReady(cfg *config.Config) bool {
	return cfg.Device.Host != "" && cfg.Device.Port != 0 && cfg.Device.User != ""
}

func (r *doctorRun) remoteChecks(dev *device.Client) {
	r.start("device probe")
	if probe, err := dev.Probe(); err != nil {
		r.add(doctorCheck{
			Status: doctorFail,
			Name:   "device probe",
			Detail: err.Error(),
		})
	} else {
		r.add(doctorCheck{
			Status: doctorPass,
			Name:   "device probe",
			Detail: fmt.Sprintf("iOS %s %s %s", probe.IOSVersion, probe.Arch, probe.Model),
		})
	}

	r.start("sudo")
	if _, errOut, code, err := dev.RunSudo("true"); err != nil {
		r.add(doctorCheck{
			Status: doctorFail,
			Name:   "sudo",
			Detail: err.Error(),
			Hint:   "bootstrap stores the SSH password so sudo can run non-interactively",
		})
	} else if code != 0 {
		r.add(doctorCheck{
			Status: doctorFail,
			Name:   "sudo",
			Detail: strings.TrimSpace(errOut),
		})
	} else {
		r.add(doctorCheck{
			Status: doctorPass,
			Name:   "sudo",
			Detail: "non-interactive sudo works",
		})
	}

	r.start("rootless path")
	if out, _, _, _ := dev.Run("test -d /var/jb && echo yes || echo no"); strings.TrimSpace(out) == "yes" {
		r.add(doctorCheck{Status: doctorPass, Name: "rootless path", Detail: "/var/jb present"})
	} else {
		r.add(doctorCheck{Status: doctorWarn, Name: "rootless path", Detail: "/var/jb not found"})
	}

	r.prereqChecks(dev)
	r.start("device output")
	r.add(doctorOutputDirCheck(dev))
	r.helperChecks(dev)
	r.autoalertChecks(dev)
	r.appChecks(dev)
}

func (r *doctorRun) prereqChecks(dev *device.Client) {
	r.start("AppSync Unified")
	if p, err := dev.LocateAppSync(); err != nil {
		r.add(doctorCheck{Status: doctorFail, Name: "AppSync Unified", Detail: err.Error()})
	} else if p == "" {
		r.add(doctorCheck{
			Status: doctorFail,
			Name:   "AppSync Unified",
			Detail: "not found",
			Hint:   "install AppSync Unified from https://lukezgd.github.io/repo",
		})
	} else {
		r.add(doctorCheck{Status: doctorPass, Name: "AppSync Unified", Detail: p})
	}

	r.start("appinst")
	if p, err := dev.LocateAppinst(); err != nil {
		r.add(doctorCheck{Status: doctorFail, Name: "appinst", Detail: err.Error()})
	} else if p == "" {
		r.add(doctorCheck{Status: doctorFail, Name: "appinst", Detail: "not found", Hint: "install appinst"})
	} else {
		r.add(doctorCheck{Status: doctorPass, Name: "appinst", Detail: p})
	}

	for _, tool := range []struct {
		name     string
		required bool
	}{
		{"zip", true},
		{"dpkg", true},
		{"uicache", false},
		{"ldid", false},
	} {
		r.start(tool.name)
		p := doctorLocateRemoteTool(dev, tool.name)
		if p == "" {
			status := doctorWarn
			if tool.required {
				status = doctorFail
			}
			r.add(doctorCheck{Status: status, Name: tool.name, Detail: "not found"})
		} else {
			r.add(doctorCheck{Status: doctorPass, Name: tool.name, Detail: p})
		}
	}
}

func doctorLocateRemoteTool(dev *device.Client, name string) string {
	script := fmt.Sprintf("sh -c 'for p in %s /var/jb/usr/bin/%s /var/jb/bin/%s /usr/bin/%s /bin/%s /usr/sbin/%s /sbin/%s; do command -v \"$p\" >/dev/null 2>&1 && { command -v \"$p\"; exit 0; }; [ -x \"$p\" ] && { echo \"$p\"; exit 0; }; done'",
		name, name, name, name, name, name, name)
	out, _, _, _ := dev.Run(script)
	return strings.TrimSpace(out)
}

func doctorOutputDirCheck(dev *device.Client) doctorCheck {
	const dir = "/var/mobile/Documents/ipadecrypt/decrypted"
	cmd := "mkdir -p /var/mobile/Documents/ipadecrypt/decrypted && " +
		"chown mobile:mobile /var/mobile/Documents/ipadecrypt /var/mobile/Documents/ipadecrypt/decrypted 2>/dev/null || true; " +
		"test -w /var/mobile/Documents/ipadecrypt/decrypted && echo yes"
	out, errOut, code, err := dev.RunSudo(cmd)
	if err != nil {
		return doctorCheck{Status: doctorFail, Name: "device output", Detail: err.Error()}
	}
	if code != 0 || strings.TrimSpace(out) != "yes" {
		return doctorCheck{Status: doctorFail, Name: "device output", Detail: strings.TrimSpace(errOut)}
	}
	return doctorCheck{Status: doctorPass, Name: "device output", Detail: dir}
}

func (r *doctorRun) helperChecks(dev *device.Client) {
	r.start("decrypt helper upload")
	helper, err := dev.EnsureHelper()
	if err != nil {
		r.add(doctorCheck{Status: doctorFail, Name: "decrypt helper", Detail: err.Error()})
		return
	}
	r.add(doctorCheck{Status: doctorPass, Name: "decrypt helper upload", Detail: helper})
	r.start("decrypt helper exec")
	if err := dev.VerifyHelper(helper); err != nil {
		r.add(doctorCheck{Status: doctorFail, Name: "decrypt helper exec", Detail: err.Error()})
	} else {
		r.add(doctorCheck{Status: doctorPass, Name: "decrypt helper exec", Detail: "usage check passed"})
	}
}

func (r *doctorRun) autoalertChecks(dev *device.Client) {
	r.start("auto-confirm tweak")
	if dev.IsAutoalertInstalled() {
		r.add(doctorCheck{Status: doctorPass, Name: "auto-confirm tweak", Detail: "installed"})
	} else {
		r.add(doctorCheck{
			Status: doctorWarn,
			Name:   "auto-confirm tweak",
			Detail: "not installed",
			Hint:   "run ipadecrypt bootstrap to install it, or tap Download manually",
		})
	}

	r.start("auto-confirm sentinel")
	out, _, _, _ := dev.RunSudo("test -e /var/mobile/.ipadecryptautoalert-arm && echo present || echo clear")
	if strings.TrimSpace(out) == "present" {
		r.add(doctorCheck{
			Status: doctorWarn,
			Name:   "auto-confirm sentinel",
			Detail: "/var/mobile/.ipadecryptautoalert-arm exists",
			Hint:   "remove it if no StoreKit install is active",
		})
	} else {
		r.add(doctorCheck{Status: doctorPass, Name: "auto-confirm sentinel", Detail: "clear"})
	}
}

func (r *doctorRun) appChecks(dev *device.Client) {
	r.start("jailbreak app package")
	out, _, code, _ := dev.RunSudo("dpkg -s com.korboy.ipadecrypt 2>/dev/null | grep -q '^Status: install ok installed' && echo yes")
	if code != 0 || strings.TrimSpace(out) != "yes" {
		r.add(doctorCheck{
			Status: doctorSkip,
			Name:   "jailbreak app",
			Detail: "app package not installed",
			Hint:   "install com.korboy.ipadecrypt_0.0.1_iphoneos-arm64.deb if you use the app",
		})
		return
	}

	r.add(doctorCheck{Status: doctorPass, Name: "jailbreak app", Detail: "package installed"})
	r.start("jailbreak app path")
	if out, _, _, _ := dev.RunSudo("test -d /var/jb/Applications/ipadecrypt.app && echo yes || echo no"); strings.TrimSpace(out) == "yes" {
		r.add(doctorCheck{Status: doctorPass, Name: "jailbreak app path", Detail: "/var/jb/Applications/ipadecrypt.app"})
	} else {
		r.add(doctorCheck{Status: doctorFail, Name: "jailbreak app path", Detail: "missing /var/jb/Applications/ipadecrypt.app"})
	}

	r.start("app daemon socket")
	if out, _, _, _ := dev.RunSudo("test -S /var/jb/var/run/ipadecryptd.sock && echo yes || echo no"); strings.TrimSpace(out) == "yes" {
		r.add(doctorCheck{Status: doctorPass, Name: "app daemon socket", Detail: "/var/jb/var/run/ipadecryptd.sock"})
	} else {
		r.add(doctorCheck{Status: doctorWarn, Name: "app daemon socket", Detail: "socket not present", Hint: "reinstall the app DEB if the app cannot decrypt"})
	}
}

func printDoctorCheck(c doctorCheck) {
	msg := c.Name
	if c.Detail != "" {
		msg += ": " + c.Detail
	}
	switch c.Status {
	case doctorPass:
		tui.OK("%s", msg)
	case doctorWarn:
		tui.Warn("%s", msg)
	case doctorFail:
		tui.Err("%s", msg)
	case doctorSkip:
		tui.Info("skip: %s", msg)
	}
	if c.Hint != "" {
		tui.Info("fix: %s", c.Hint)
	}
}

func printDoctorSummary(checks []doctorCheck) {
	tui.Spacer()
	if doctorExitCode(checks) == 0 {
		tui.OK("doctor complete: %s", doctorSummary(checks))
	} else {
		tui.Err("doctor found problems: %s", doctorSummary(checks))
	}
}

func doctorExitCode(checks []doctorCheck) int {
	for _, c := range checks {
		if c.Status == doctorFail {
			return 1
		}
	}
	return 0
}

func doctorSummary(checks []doctorCheck) string {
	counts := map[doctorStatus]int{}
	for _, c := range checks {
		counts[c.Status]++
	}
	return fmt.Sprintf("%d ok, %d warn, %d fail, %d skip",
		counts[doctorPass], counts[doctorWarn], counts[doctorFail], counts[doctorSkip])
}

func parseDoctorKV(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}
