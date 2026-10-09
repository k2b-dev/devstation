package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
	"unicode"
)

type Options struct {
	Project, Name  string
	Title, Link    string
	Keep           bool
	AllowSensitive bool
	Paths          []string
}

type Result struct {
	Meta     Meta
	Version  Version
	Skipped  []string
	Warnings []string
	Removed  []string // expired artifacts pruned by this command
}

// Publish copies the inputs as a new immutable version. Copying runs without
// the store lock; only finalizing is serialized.
func (s Store) Publish(o Options) (Result, error) {
	var r Result
	if !label.MatchString(o.Project) || !label.MatchString(o.Name) {
		return r, fmt.Errorf("--project and --name must be lowercase DNS labels (1–63 letters, digits or hyphens)")
	}
	if err := checkTitle(o.Title); err != nil {
		return r, err
	}
	if err := checkLink(o.Link); err != nil {
		return r, err
	}
	in, err := collect(o.Paths, o.AllowSensitive)
	if err != nil {
		return r, err
	}
	r.Skipped = in.skipped
	stage, err := s.tempDir("tmp")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(stage)
	payload := filepath.Join(stage, "payload")
	files, err := copyInto(payload, in.inputs)
	if err != nil {
		return r, err
	}
	v := Version{Files: files, Bytes: in.bytes}
	v.Kind, v.Entry = classify(files)
	var ds docSet
	if v.Kind != "site" {
		// Markdown can be slow to render; do it before taking the lock.
		if ds, err = prepareDocs(files, payload); err != nil {
			return r, err
		}
		r.Warnings = ds.warnings
	}

	unlock, err := s.lock()
	if err != nil {
		return r, err
	}
	defer s.cleanup()
	defer unlock()
	now := s.now()
	r.Removed, err = s.prune(now, o.Project+"/"+o.Name)
	if err != nil {
		return r, err
	}
	m, err := s.readMeta(o.Project, o.Name)
	if errors.Is(err, os.ErrNotExist) {
		m = Meta{Project: o.Project, Name: o.Name, Created: now}
	} else if err != nil {
		return r, err
	}
	v.N, err = s.nextVersion(m)
	if err != nil {
		return r, err
	}
	v.At = now
	m.Versions = append(m.Versions, v)
	m.Updated = now
	if o.Title != "" {
		m.Title = o.Title
	}
	if o.Link != "" {
		m.Link = o.Link
	}
	m.Keep = m.Keep || o.Keep

	if v.Kind == "site" {
		if v.Entry != "index.html" {
			page, err := renderRedirect(heading(m), "./"+escapePath(v.Entry))
			if err != nil {
				return r, err
			}
			if err = os.WriteFile(filepath.Join(payload, "index.html"), page, 0600); err != nil {
				return r, err
			}
		}
	} else {
		page, err := renderVersion(m, v.N, false, ds)
		if err != nil {
			return r, err
		}
		if err = os.WriteFile(filepath.Join(payload, "index.html"), page, 0600); err != nil {
			return r, err
		}
	}
	if err = os.MkdirAll(filepath.Join(s.artifact(o.Project, o.Name), "v"), 0700); err != nil {
		return r, err
	}
	if err = os.Rename(payload, s.versionDir(o.Project, o.Name, v.N)); err != nil {
		return r, err
	}
	if len(m.Versions) > 1 {
		prev := m.Versions[len(m.Versions)-2].N
		page, err := renderCompare(m, prev, v.N)
		if err != nil {
			return r, err
		}
		dir := filepath.Join(s.artifact(o.Project, o.Name), "compare", fmt.Sprintf("%d-%d", prev, v.N))
		if err = os.MkdirAll(dir, 0700); err != nil {
			return r, err
		}
		if err = writeFileAtomic(filepath.Join(dir, "index.html"), page); err != nil {
			return r, err
		}
	}
	if reviewable(v) {
		if err = s.writeReview(m, v.N); err != nil {
			return r, err
		}
	}
	if err = s.writeMeta(m); err != nil {
		return r, err
	}
	if err = s.writeCurrent(m, ds); err != nil {
		return r, err
	}
	if err = s.writeIndexes(o.Project); err != nil {
		return r, err
	}
	r.Meta, r.Version = m, v
	return r, nil
}

// classify decides how the stable URL presents a version. A top-level
// index.html, or one single file that is neither Markdown nor media, is
// served as-is ("site").
func classify(files []File) (kind, entry string) {
	for _, f := range files {
		if f.Path == "index.html" {
			return "site", "index.html"
		}
	}
	if len(files) == 1 && !isMarkdown(files[0].Path) && !isMedia(files[0].Path) {
		return "site", files[0].Path
	}
	media, docs := 0, len(renderedDocs(files))
	for _, f := range files {
		if isMedia(f.Path) {
			media++
		}
	}
	switch {
	case docs > 0:
		return "plan", ""
	case media > 0:
		return "gallery", ""
	}
	return "files", ""
}

// nextVersion also counts version directories without metadata, which an
// interrupted publish can leave behind, so numbers are never reused.
func (s Store) nextVersion(m Meta) (int, error) {
	next := 1
	if len(m.Versions) > 0 {
		next = m.latest().N + 1
	}
	entries, err := os.ReadDir(filepath.Join(s.artifact(m.Project, m.Name), "v"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && n >= next {
			next = n + 1
		}
	}
	return next, nil
}

func (s Store) writeMeta(m Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.metaPath(m.Project, m.Name), append(data, '\n'))
}

// writeCurrent renders the stable page at <project>/<name>/.
func (s Store) writeCurrent(m Meta, ds docSet) error {
	v := m.latest()
	var page []byte
	var err error
	if v.Kind == "site" {
		target := fmt.Sprintf("./v/%d/", v.N)
		if v.Entry != "index.html" {
			target += escapePath(v.Entry)
		}
		if reviewable(v) { // mockups open where they can be commented
			target = fmt.Sprintf("./review/%d/", v.N)
		}
		page, err = renderRedirect(heading(m), target)
	} else {
		page, err = renderVersion(m, v.N, true, ds)
	}
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.artifact(m.Project, m.Name), "index.html"), page)
}

// writeIndexes regenerates the overview of the given projects and the root.
// A project without any artifact directory is removed. Callers hold the lock.
func (s Store) writeIndexes(projects ...string) error {
	for _, p := range projects {
		metas, _, dirs, err := s.projectMetas(p)
		if err != nil {
			return err
		}
		if dirs == 0 {
			if err = s.discard(s.project(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		page, err := renderProject(p, metas)
		if err != nil {
			return err
		}
		if err = writeFileAtomic(filepath.Join(s.project(p), "index.html"), page); err != nil {
			return err
		}
	}
	names, err := s.projects()
	if err != nil {
		return err
	}
	var entries []projectEntry
	for _, p := range names {
		metas, _, _, err := s.projectMetas(p)
		if err != nil {
			return err
		}
		if len(metas) == 0 {
			continue
		}
		entries = append(entries, projectEntry{Name: p, Href: "./" + p + "/", Count: len(metas), Updated: metas[0].Updated})
	}
	page, err := renderRoot(entries)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.Site(), 0700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.Site(), "index.html"), page)
}

// prune removes expired artifacts except the one being republished, and
// directories left behind by a first publish that was interrupted.
func (s Store) prune(now time.Time, except string) ([]string, error) {
	projects, err := s.projects()
	if err != nil {
		return nil, err
	}
	var removed, touched []string
	for _, p := range projects {
		entries, err := os.ReadDir(s.project(p))
		if err != nil {
			return nil, err
		}
		changed := false
		for _, e := range entries {
			ref := p + "/" + e.Name()
			if !e.IsDir() || ref == except {
				continue
			}
			m, err := s.readMeta(p, e.Name())
			switch {
			case errors.Is(err, os.ErrNotExist):
				info, ierr := e.Info()
				if ierr != nil || now.Sub(info.ModTime()) < staleTemp {
					continue
				}
			case err != nil:
				continue // damaged metadata: leave it for an explicit unpublish
			case m.Keep || now.Before(m.Expires()):
				continue
			default:
				removed = append(removed, ref)
			}
			if err = s.discard(s.artifact(p, e.Name())); err != nil {
				return nil, err
			}
			changed = true
		}
		if changed {
			touched = append(touched, p)
		}
	}
	if len(touched) > 0 {
		if err = s.writeIndexes(touched...); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

// Keep marks an artifact as kept until it is unpublished.
func (s Store) Keep(p, n string) (Meta, error) {
	unlock, err := s.lock()
	if err != nil {
		return Meta{}, err
	}
	defer s.cleanup()
	defer unlock()
	m, err := s.readMeta(p, n)
	if errors.Is(err, os.ErrNotExist) {
		return m, fmt.Errorf("artifact %s/%s does not exist", p, n)
	} else if err != nil {
		return m, err
	}
	if _, err = s.prune(s.now(), p+"/"+n); err != nil {
		return m, err
	}
	m.Keep = true
	if err = s.writeMeta(m); err != nil {
		return m, err
	}
	return m, s.writeIndexes(p)
}

// Unpublish removes artifacts with all versions, then expired ones. With no
// refs it only removes the expired ones. An artifact with damaged metadata can
// still be removed by name.
func (s Store) Unpublish(refs []string) ([]string, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer s.cleanup()
	defer unlock()
	var removed, projects []string
	for _, ref := range unique(refs) {
		p, n, err := ParseRef(ref)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(s.artifact(p, n)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("artifact %s does not exist", ref)
		}
	}
	for _, ref := range unique(refs) {
		p, n, _ := ParseRef(ref)
		if err = s.discard(s.artifact(p, n)); err != nil {
			break
		}
		removed = append(removed, ref)
		projects = append(projects, p)
	}
	// Rewrite overviews even after a partial failure so they never link
	// removed artifacts.
	if ierr := s.writeIndexes(unique(projects)...); err == nil {
		err = ierr
	}
	if err != nil {
		return removed, err
	}
	expired, err := s.prune(s.now(), "")
	return append(removed, expired...), err
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func checkTitle(t string) error {
	if len([]rune(t)) > 200 {
		return fmt.Errorf("--title is limited to 200 characters")
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return fmt.Errorf("--title must be a single line of text")
		}
	}
	return nil
}

func checkLink(l string) error {
	if l == "" {
		return nil
	}
	u, err := url.Parse(l)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("--link must be an absolute http(s) URL")
	}
	return nil
}

// URLs of an artifact below the route's base URL.
func ArtifactURL(base string, m Meta) string { return base + "/" + m.Project + "/" + m.Name + "/" }
func VersionURL(base string, m Meta, k int) string {
	return ArtifactURL(base, m) + path.Join("v", strconv.Itoa(k)) + "/"
}
func CompareURL(base string, m Meta) string {
	if len(m.Versions) < 2 {
		return ""
	}
	a, b := m.Versions[len(m.Versions)-2].N, m.latest().N
	return ArtifactURL(base, m) + "compare/" + strconv.Itoa(a) + "-" + strconv.Itoa(b) + "/"
}

// Summary is the one-line description used in listings.
func Summary(m Meta) string { return summary(m.latest()) }

// RebuildIndexes rewrites the root, every project overview and every
// compare page with the current templates, so a new release changes existing
// pages without a publish.
func (s Store) RebuildIndexes() error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	projects, err := s.projects()
	if err != nil {
		return err
	}
	for _, p := range projects {
		metas, _, _, err := s.projectMetas(p)
		if err != nil {
			return err
		}
		for _, m := range metas {
			if err = s.rewriteCompares(m); err != nil {
				return err
			}
		}
	}
	return s.writeIndexes(projects...)
}

// rewriteCompares re-renders the compare pages that already exist between
// consecutive versions.
func (s Store) rewriteCompares(m Meta) error {
	for i := 1; i < len(m.Versions); i++ {
		a, b := m.Versions[i-1].N, m.Versions[i].N
		path := filepath.Join(s.artifact(m.Project, m.Name), "compare", fmt.Sprintf("%d-%d", a, b), "index.html")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		page, err := renderCompare(m, a, b)
		if err != nil {
			return err
		}
		if err = writeFileAtomic(path, page); err != nil {
			return err
		}
	}
	return nil
}
