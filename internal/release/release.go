// Package release installs checksum-verified, versioned Linux release binaries.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const repo = "https://github.com/k2b-dev/devstation"

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func fetch(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "devstation-updater")
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" {
			return fmt.Errorf("unsafe redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("download exceeds size limit")
	}
	return data, nil
}

func checksum(manifest []byte, asset string) (string, error) {
	found := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != asset {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("duplicate checksum for %s", asset)
		}
		decoded, err := hex.DecodeString(f[0])
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid checksum")
		}
		found = strings.ToLower(f[0])
	}
	if found == "" {
		return "", fmt.Errorf("missing checksum for %s", asset)
	}
	return found, nil
}

func replace(path string, data []byte, want string) error {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("checksum mismatch; installed binary unchanged")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dev-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0755); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func Update(version string, out io.Writer) error {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return fmt.Errorf("updates support Linux amd64 and arm64 only")
	}
	path, err := os.Executable()
	if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	l, err := os.OpenFile(path+".update.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("installation directory is not writable: %w", err)
	}
	defer l.Close()
	if err = syscall.Flock(int(l.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another update is running")
	}
	defer syscall.Flock(int(l.Fd()), syscall.LOCK_UN)
	return installRelease(version, path, runtime.GOARCH, out, fetch)
}

func installRelease(version, path, arch string, out io.Writer, download func(context.Context, string, int64) ([]byte, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if version == "latest" {
		data, e := download(ctx, "https://api.github.com/repos/k2b-dev/devstation/releases/latest", 1<<20)
		if e != nil {
			return e
		}
		var r struct {
			Tag string `json:"tag_name"`
		}
		if e = json.Unmarshal(data, &r); e != nil {
			return e
		}
		version = r.Tag
	}
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("release version must be vX.Y.Z")
	}
	asset := "devstation_" + version + "_linux_" + arch
	base := repo + "/releases/download/" + version + "/"
	manifest, err := download(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	want, err := checksum(manifest, asset)
	if err != nil {
		return err
	}
	data, err := download(ctx, base+asset, 64<<20)
	if err != nil {
		return err
	}
	if err = replace(path, data, want); err != nil {
		return err
	}
	fmt.Fprintln(out, "Installed", version, "at", path)
	return nil
}
