package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const RemoteRoot = "/var/mobile/Media/ipadecrypt"

var ErrSudoPasswordRejected = errors.New("sudo password rejected")

type Client struct {
	cfg  config.Device
	ssh  *ssh.Client
	sftp *sftp.Client
}

func Connect(ctx context.Context, dev config.Device) (*Client, error) {
	auth, err := sshAuthMethods(dev.Auth)
	if err != nil {
		return nil, err
	}

	cfg := &ssh.ClientConfig{
		User:            dev.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(dev.Host, strconv.Itoa(dev.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}

	sshClient := ssh.NewClient(sshConn, chans, reqs)

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("sftp open: %w", err)
	}

	return &Client{cfg: dev, ssh: sshClient, sftp: sftpClient}, nil
}

func sshAuthMethods(a config.DeviceAuth) ([]ssh.AuthMethod, error) {
	if a.Kind == "key" {
		path, err := expandUser(a.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("expand key path: %w", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", path, err)
		}

		var signer ssh.Signer
		if a.KeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(a.KeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(data)
		}
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", path, err)
		}

		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}

	return []ssh.AuthMethod{ssh.Password(a.Password)}, nil
}

func expandUser(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func (c *Client) Close() {
	c.sftp.Close()
	c.ssh.Close()
}

func (c *Client) Run(cmd string) (string, string, int, error) {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("new session: %w", err)
	}

	defer sess.Close()

	var so, se bytes.Buffer
	sess.Stdout = &so
	sess.Stderr = &se

	err = sess.Run(cmd)

	exit := 0
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitStatus()
			err = nil
		} else {
			return so.String(), se.String(), -1, err
		}
	}

	return so.String(), se.String(), exit, nil
}

func (c *Client) RunSudo(cmd string) (string, string, int, error) {
	return c.RunSudoStream(cmd, nil, nil)
}

func (c *Client) RunSudoStream(cmd string, stdoutW, stderrW io.Writer) (string, string, int, error) {
	full := "sudo -S -p '' " + cmd

	sess, err := c.ssh.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return "", "", -1, fmt.Errorf("stdin pipe: %w", err)
	}

	go func() {
		stdin.Write([]byte(c.cfg.Auth.Password + "\n"))
		stdin.Close()
	}()

	var soBuf, seBuf bytes.Buffer
	if stdoutW != nil {
		sess.Stdout = io.MultiWriter(stdoutW, &soBuf)
	} else {
		sess.Stdout = &soBuf
	}
	if stderrW != nil {
		sess.Stderr = io.MultiWriter(stderrW, &seBuf)
	} else {
		sess.Stderr = &seBuf
	}

	err = sess.Run(full)

	exit := 0
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitStatus()
			err = nil
		} else {
			return soBuf.String(), seBuf.String(), -1, err
		}
	}

	if s := seBuf.String(); strings.Contains(s, "incorrect password") ||
		strings.Contains(s, "try again") {
		return soBuf.String(), s, exit, ErrSudoPasswordRejected
	}

	return soBuf.String(), seBuf.String(), exit, nil
}

func (c *Client) Mkdir(path string) error {
	if err := c.sftp.MkdirAll(path); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}

	return nil
}

func (c *Client) Upload(src io.Reader, dst string, mode os.FileMode) error {
	if err := c.Mkdir(path.Dir(dst)); err != nil {
		return err
	}

	f, err := c.sftp.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	if _, err := f.ReadFromWithConcurrency(src, 0); err != nil {
		f.Close()
		c.Remove(dst)
		return fmt.Errorf("upload %s: %w", dst, err)
	}

	if err := f.Close(); err != nil {
		c.Remove(dst)
		return fmt.Errorf("close %s: %w", dst, err)
	}

	if mode != 0 {
		if err := c.sftp.Chmod(dst, mode); err != nil {
			c.Remove(dst)
			return fmt.Errorf("chmod %s: %w", dst, err)
		}
	}

	return nil
}

// Download streams the remote file at src into dst. Caller owns dst (open,
// close, atomic rename if needed). Wrap dst for progress.
func (c *Client) Download(src string, dst io.Writer) error {
	f, err := c.sftp.Open(src)
	if err != nil {
		return fmt.Errorf("open remote %s: %w", src, err)
	}

	defer f.Close()

	if _, err := f.WriteTo(dst); err != nil {
		return fmt.Errorf("download %s: %w", src, err)
	}

	return nil
}

func (c *Client) Stat(p string) (os.FileInfo, error) {
	return c.sftp.Stat(p)
}

func (c *Client) Exists(path string) bool {
	_, err := c.sftp.Stat(path)
	return err == nil
}

func (c *Client) Remove(path string) error {
	return c.sftp.Remove(path)
}
