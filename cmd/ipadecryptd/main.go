package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const defaultSocketPath = "/var/jb/var/run/ipadecryptd.sock"

func main() {
	socketPath := socketPath()
	_ = os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()
	_ = os.Chmod(socketPath, 0o666)

	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handle(c)
	}
}

func socketPath() string {
	if p := strings.TrimSpace(os.Getenv("IPADECRYPTD_SOCKET")); p != "" {
		return p
	}
	return defaultSocketPath
}

func handle(c net.Conn) {
	defer c.Close()

	r := bufio.NewReader(c)
	helper, err := readLine(r)
	if err != nil {
		emit(c, "event=daemon phase=failed reason=bad_request")
		return
	}
	bundleID, err := readLine(r)
	if err != nil {
		emit(c, "event=daemon phase=failed reason=bad_request")
		return
	}
	bundlePath, err := readLine(r)
	if err != nil {
		emit(c, "event=daemon phase=failed reason=bad_request")
		return
	}
	outIPA, err := readLine(r)
	if err != nil {
		emit(c, "event=daemon phase=failed reason=bad_request")
		return
	}

	if err := ensureBundleExecutables(bundlePath); err != nil {
		emit(c, "event=daemon phase=failed reason=prepare_executables message=%q", err.Error())
		return
	}

	cmd := exec.Command(helper, bundleID, bundlePath, outIPA)
	cmd.Dir = "/var/root"
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/var/jb/usr/bin:/var/jb/usr/sbin:/usr/bin:/bin:/usr/sbin:/sbin"
	}
	cmd.Env = append(os.Environ(),
		"PATH="+pathEnv,
		"HOME=/var/root",
		"TMPDIR=/tmp",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(c, "event=daemon phase=failed reason=stdout_pipe")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		emit(c, "event=daemon phase=failed reason=stderr_pipe")
		return
	}
	if err := cmd.Start(); err != nil {
		emit(c, "event=daemon phase=failed reason=start message=%q", err.Error())
		return
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)
	go copyLines(&wg, &mu, c, stdout, false)
	go copyLines(&wg, &mu, c, stderr, true)
	wg.Wait()

	err = cmd.Wait()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	mu.Lock()
	emit(c, "event=daemon phase=exit code=%d", code)
	fmt.Fprintf(c, "__ipadecryptd_exit %d\n", code)
	mu.Unlock()
}

func ensureBundleExecutables(bundlePath string) error {
	script := `bundle=$1
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
	cmd := exec.Command("sh", "-c", script, "ipadecryptd-prepare", bundlePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readLine(r *bufio.Reader) (string, error) {
	s, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func copyLines(wg *sync.WaitGroup, mu *sync.Mutex, dst io.Writer, src io.Reader, stderr bool) {
	defer wg.Done()
	s := bufio.NewScanner(src)
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)
	for s.Scan() {
		line := s.Text()
		mu.Lock()
		if stderr {
			emit(dst, "event=stderr line=%q", line)
		} else {
			fmt.Fprintln(dst, line)
		}
		mu.Unlock()
	}
}

func emit(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "@evt "+format+"\n", args...)
}
