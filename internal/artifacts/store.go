// Package artifacts publishes immutable copies of files as static pages that
// the Devstation Caddy instance serves. Rendering happens at publish time;
// Caddy only serves files.
package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Lifetime is how long an artifact stays after its last publish unless kept.
const Lifetime = 14 * 24 * time.Hour

const (
	lockWait  = 30 * time.Second
	staleTemp = time.Hour
)

var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Store is the artifact directory. Only Site is served.
type Store struct {
	Root string
	Now  func() time.Time
}

// DefaultRoot follows the XDG data directory convention.
func DefaultRoot() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("XDG_DATA_HOME must be an absolute path")
	}
	return filepath.Join(base, "devstation", "artifacts"), nil
}

func (s Store) Site() string                { return filepath.Join(s.Root, "site") }
func (s Store) project(p string) string     { return filepath.Join(s.Site(), p) }
func (s Store) artifact(p, n string) string { return filepath.Join(s.Site(), p, n) }
func (s Store) metaPath(p, n string) string { return filepath.Join(s.artifact(p, n), "artifact.json") }
func (s Store) versionDir(p, n string, k int) string {
	return filepath.Join(s.artifact(p, n), "v", fmt.Sprint(k))
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Meta is stored as artifact.json next to the artifact's pages.
type Meta struct {
	Project  string    `json:"project"`
	Name     string    `json:"name"`
	Title    string    `json:"title,omitempty"`
	Link     string    `json:"link,omitempty"`
	Keep     bool      `json:"keep"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Versions []Version `json:"versions"`
}

type Version struct {
	N     int       `json:"n"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`            // gallery, plan, files or site
	Entry string    `json:"entry,omitempty"` // site mode: file the stable URL opens
	Bytes int64     `json:"bytes"`
	Files []File    `json:"files"`
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Expires reports the deletion time, or zero when the artifact is kept.
func (m Meta) Expires() time.Time {
	if m.Keep {
		return time.Time{}
	}
	return m.Updated.Add(Lifetime)
}

func (m Meta) latest() Version { return m.Versions[len(m.Versions)-1] }

func (s Store) readMeta(p, n string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(s.metaPath(p, n))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("invalid metadata for %s/%s (remove it with `dev unpublish %s/%s`): %w", p, n, p, n, err)
	}
	if m.Project != p || m.Name != n || len(m.Versions) == 0 {
		return m, fmt.Errorf("invalid metadata for %s/%s; remove it with `dev unpublish %s/%s`", p, n, p, n)
	}
	return m, nil
}

// List returns all artifacts, optionally of one project, newest first, and
// the references of artifacts whose metadata is damaged.
func (s Store) List(project string) ([]Meta, []string, error) {
	projects, err := s.projects()
	if err != nil {
		return nil, nil, err
	}
	all, damaged := []Meta{}, []string{}
	for _, p := range projects {
		if project != "" && p != project {
			continue
		}
		metas, bad, _, err := s.projectMetas(p)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, metas...)
		for _, n := range bad {
			damaged = append(damaged, p+"/"+n)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Updated.After(all[j].Updated) })
	return all, damaged, nil
}

func (s Store) projects() ([]string, error) {
	entries, err := os.ReadDir(s.Site())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && label.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// projectMetas reads the artifacts of a project. Directories without valid
// metadata never block the others: damaged ones are reported (removable with
// unpublish), and dirs counts every artifact directory, listed or not.
func (s Store) projectMetas(p string) (metas []Meta, damaged []string, dirs int, err error) {
	entries, err := os.ReadDir(s.project(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, 0, nil
	}
	if err != nil {
		return nil, nil, 0, err
	}
	for _, e := range entries {
		if !e.IsDir() || !label.MatchString(e.Name()) {
			continue
		}
		dirs++
		m, err := s.readMeta(p, e.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue // interrupted first publish; removed by prune
		}
		if err != nil {
			damaged = append(damaged, e.Name())
			continue
		}
		metas = append(metas, m)
	}
	sort.SliceStable(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	return metas, damaged, dirs, nil
}

// lock serializes finalize, prune and delete. Copying happens outside it.
func (s Store) lock() (func(), error) {
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("artifact store busy: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tempDir creates a private directory on the store's filesystem so that
// renames into the site are atomic.
func (s Store) tempDir(kind string) (string, error) {
	dir := filepath.Join(s.Root, kind)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, "")
}

// discard moves a path out of the site in one rename; cleanup removes it
// after unlock.
func (s Store) discard(path string) error {
	trash := filepath.Join(s.Root, "trash")
	if err := os.MkdirAll(trash, 0700); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(trash, fmt.Sprintf("%s-%d-%d", filepath.Base(path), os.Getpid(), time.Now().UnixNano())))
}

// cleanup removes discarded entries and stale staging directories. It runs
// without the lock: every trash entry is complete garbage, and publishes in
// other processes only use fresh tmp entries. The directories themselves stay,
// so a concurrent discard or tempDir never loses its parent.
func (s Store) cleanup() {
	trash := filepath.Join(s.Root, "trash")
	entries, _ := os.ReadDir(trash)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(trash, e.Name()))
	}
	entries, _ = os.ReadDir(filepath.Join(s.Root, "tmp"))
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && s.now().Sub(info.ModTime()) > staleTemp {
			_ = os.RemoveAll(filepath.Join(s.Root, "tmp", e.Name()))
		}
	}
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
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
	if err = os.Chmod(f.Name(), 0600); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ParseRef splits PROJECT/NAME.
func ParseRef(ref string) (string, string, error) {
	p, n, ok := strings.Cut(ref, "/")
	if !ok || !label.MatchString(p) || !label.MatchString(n) {
		return "", "", fmt.Errorf("artifact must be PROJECT/NAME with lowercase DNS labels, got %q", ref)
	}
	return p, n, nil
}
