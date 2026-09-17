package upload

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eordano/recgo/internal/config"
)

func httpUpload(endpoint, file string) (string, error) {
	base := filepath.Base(file)
	f, err := os.Open(file)
	if err != nil {
		return "", fmt.Errorf("open recording: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat recording: %w", err)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("bad upload url %q: %w", endpoint, err)
	}
	q := u.Query()
	q.Set("name", base)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPost, u.String(), f)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", "application/octet-stream")

	client := &http.Client{Timeout: 2 * time.Hour}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http upload: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upload rejected (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return fmt.Sprintf("%s (%s)", endpoint, strings.TrimSpace(string(body))), nil
}

func sshCommand(key string) string {
	c := "ssh -o StrictHostKeyChecking=accept-new"
	if key != "" {
		c += " -i " + key + " -o IdentitiesOnly=yes"
	}
	return c
}

func destDir(cfg config.UploadConfig, file string) string {
	target := strings.TrimRight(cfg.Target, "/")
	if cfg.OrganizeByMonth {
		base := filepath.Base(file)
		if len(base) >= 7 && base[4] == '.' {
			return target + "/" + base[:7] + "/"
		}
	}
	return target + "/"
}

// SyncDir mirrors a whole session directory to cfg.Target. Upload() moves a
// single recording and rsyncs it flat into the target, which for a recgo-tab
// session would scatter SESSION.md and its siblings across the target root;
// this keeps the session as one named directory. It is a no-op with no target
// configured, so a host that only has an [upload] url (single files over HTTP)
// stays exactly as it was.
func SyncDir(cfg config.UploadConfig, dir string) (string, error) {
	if cfg.Target == "" {
		return "", nil
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("session directory not found: %s", dir)
	}
	target := strings.TrimRight(cfg.Target, "/")
	ssh := sshCommand(cfg.SSHKey)

	if host, remote, ok := strings.Cut(target, ":"); ok {
		fields := strings.Fields(ssh)
		args := append(fields[1:], host, "mkdir", "-p", remote)
		if out, err := exec.Command(fields[0], args...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("mkdir %s: %v: %s", remote, err, strings.TrimSpace(string(out)))
		}
	}

	// No trailing slash on the source: rsync then creates the directory by
	// name under the target instead of emptying its contents into it.
	cmd := exec.Command("rsync", "-aH", "--timeout=120", "--chmod=D755,F644",
		"-e", ssh, strings.TrimRight(dir, "/"), target+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("rsync: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return target + "/" + filepath.Base(dir), nil
}

// Display drops the ssh login from an rsync destination so it reads as
// host:/path -- what a human would type, and it keeps a login name out of a
// line that a recgo-tab session may itself be recording.
func Display(dest string) string {
	host, path, ok := strings.Cut(dest, ":")
	if !ok {
		return dest
	}
	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}
	return host + ":" + path
}

// LocalMirror is the path a synced session has on this machine when the
// rsync destination's directory is also mounted here (a shared session root
// mounted at the same absolute path on every host), so the line printed
// after a sync is one that pastes into an agent on any host. Empty when the
// destination has no host part or its path is not a directory here.
func LocalMirror(dest string) string {
	_, path, ok := strings.Cut(dest, ":")
	if !ok || !strings.HasPrefix(path, "/") {
		return ""
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return ""
	}
	return path
}

func Upload(cfg config.UploadConfig, file string) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}
	if _, err := os.Stat(file); err != nil {
		return "", fmt.Errorf("recording not found: %w", err)
	}
	if cfg.URL != "" {
		return httpUpload(cfg.URL, file)
	}
	if cfg.Target == "" {
		return "", fmt.Errorf("upload enabled but neither url nor target is set")
	}

	dest := destDir(cfg, file)
	ssh := sshCommand(cfg.SSHKey)

	if host, remote, ok := strings.Cut(dest, ":"); ok {
		args := append(strings.Fields(ssh)[1:], host, "mkdir", "-p", remote)
		mk := exec.Command(strings.Fields(ssh)[0], args...)
		if out, err := mk.CombinedOutput(); err != nil {
			return "", fmt.Errorf("mkdir %s: %v: %s", remote, err, strings.TrimSpace(string(out)))
		}
	}

	cmd := exec.Command("rsync", "-aH", "-e", ssh, file, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("rsync: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return dest + filepath.Base(file), nil
}
