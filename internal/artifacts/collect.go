package artifacts

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"  // register decoder for image dimensions
	_ "image/jpeg" // register decoder for image dimensions
	_ "image/png"  // register decoder for image dimensions
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxFiles         = 2000
	maxBytes         = 512 << 20
	maxMarkdown      = 2 << 20
	maxMarkdownTotal = 8 << 20
)

var (
	imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true}
	videoExt = map[string]bool{".mp4": true, ".webm": true}
)

func isImage(p string) bool { return imageExt[strings.ToLower(path.Ext(p))] }
func isVideo(p string) bool { return videoExt[strings.ToLower(path.Ext(p))] }
func isMedia(p string) bool { return isImage(p) || isVideo(p) }
func isMarkdown(p string) bool {
	e := strings.ToLower(path.Ext(p))
	return e == ".md" || e == ".markdown"
}

// sensitive reports paths that commonly hold credentials or private data:
// hidden files, keys, dumps, databases, browser storage and traces, and data
// files whose path mentions tokens, cookies, secrets or credentials. Pages,
// styles, scripts and media may use those words freely, as in
// design-tokens.css or refresh-token-flow.md.
func sensitive(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	lower := strings.ToLower(rel)
	base := path.Base(lower)
	for _, ext := range []string{".gz", ".zst", ".xz", ".bz2"} {
		base = strings.TrimSuffix(base, ext)
	}
	for _, ext := range []string{".env", ".local", ".dump", ".sql", ".sqlite", ".sqlite3", ".db", ".har", ".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx", ".ppk", ".ovpn", ".cookie"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, prefix := range []string{"id_rsa", "id_ecdsa", "id_ed25519"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	for _, part := range []string{".tfstate", "storagestate", "storage-state"} {
		if strings.Contains(base, part) {
			return true
		}
	}
	if base == "auth.json" || base == "trace.zip" {
		return true
	}
	if !dataExt[path.Ext(base)] {
		return false
	}
	for _, word := range []string{"token", "cookie", "secret", "credential", "password", "passwd", "apikey", "api_key", "api-key", "session", "kubeconfig"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// isTraceArchive reports zip files with browser traces, such as those in a
// Playwright report; they record request cookies and headers.
func isTraceArchive(src string) bool {
	if !strings.EqualFold(filepath.Ext(src), ".zip") {
		return false
	}
	r, err := zip.OpenReader(src)
	if err != nil {
		return false
	}
	defer r.Close()
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, ".trace") || strings.HasSuffix(f.Name, ".network") {
			return true
		}
	}
	return false
}

// dataExt lists extensions of files that hold data rather than presentation.
var dataExt = map[string]bool{"": true, ".json": true, ".txt": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true, ".csv": true, ".xml": true, ".log": true}

// renderedDocs returns the Markdown files rendered as sections, index.md and
// README.md first, then in natural order. Files over maxMarkdown, and files
// beyond maxMarkdownTotal together, are listed as files instead, which bounds
// the time the store lock is held.
func renderedDocs(files []File) []File {
	var docs []File
	for _, f := range files {
		if isMarkdown(f.Path) && f.Size <= maxMarkdown {
			docs = append(docs, f)
		}
	}
	rank := func(p string) int {
		switch strings.ToLower(p) {
		case "index.md", "readme.md":
			return 0
		}
		return 1
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if a, b := rank(docs[i].Path), rank(docs[j].Path); a != b {
			return a < b
		}
		return naturalLess(docs[i].Path, docs[j].Path)
	})
	var total int64
	for i, d := range docs {
		if total += d.Size; total > maxMarkdownTotal {
			return docs[:i]
		}
	}
	return docs
}

type input struct {
	src string // absolute source path
	rel string // slash-separated destination below the version root
}

type collected struct {
	inputs  []input
	skipped []string
	bytes   int64
}

// collect resolves the explicitly passed paths. A single directory publishes
// its contents; several arguments are placed under their base names.
// Directory walks skip hidden entries and symlinks.
func collect(paths []string, allowSensitive bool) (collected, error) {
	var c collected
	if len(paths) == 0 {
		return c, fmt.Errorf("pass at least one file or directory")
	}
	seen := map[string]bool{}
	var hiddenArgs []string
	add := func(src, rel string, size int64) error {
		if !utf8.ValidString(rel) {
			return fmt.Errorf("file name %q is not valid UTF-8; rename it before publishing", rel)
		}
		if seen[rel] {
			return fmt.Errorf("two inputs would both be published as %q", rel)
		}
		seen[rel] = true
		c.inputs = append(c.inputs, input{src, rel})
		c.bytes += size
		if len(c.inputs) > maxFiles {
			return fmt.Errorf("more than %d files; publish a smaller selection", maxFiles)
		}
		if c.bytes > maxBytes {
			return fmt.Errorf("more than %d MiB; publish a smaller selection", maxBytes>>20)
		}
		return nil
	}
	for _, arg := range paths {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return c, err
		}
		// Keep the name as given, even when the argument is a symlink.
		given := filepath.Base(abs)
		// A hidden argument, or a file inside a hidden folder such as
		// playwright/.auth/user.json, counts as hidden too.
		if strings.HasPrefix(given, ".") || strings.HasPrefix(filepath.Base(filepath.Dir(abs)), ".") {
			hiddenArgs = append(hiddenArgs, arg)
		}
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return c, fmt.Errorf("input: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return c, fmt.Errorf("input: %w", err)
		}
		if info.Mode().IsRegular() {
			if err = add(abs, given, info.Size()); err != nil {
				return c, err
			}
			continue
		}
		if !info.IsDir() {
			return c, fmt.Errorf("input %s is not a regular file or directory", arg)
		}
		prefix := ""
		if len(paths) > 1 {
			prefix = given + "/"
		}
		err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if p == abs {
				return nil
			}
			rel, err := filepath.Rel(abs, p)
			if err != nil {
				return err
			}
			rel = prefix + filepath.ToSlash(rel)
			if strings.HasPrefix(d.Name(), ".") {
				c.skipped = append(c.skipped, rel)
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				c.skipped = append(c.skipped, rel) // symlinks, sockets, devices
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			return add(p, rel, fi.Size())
		})
		if err != nil {
			return c, err
		}
	}
	if len(c.inputs) == 0 {
		return c, fmt.Errorf("nothing to publish: no visible regular files")
	}
	refused := hiddenArgs
	for _, in := range c.inputs {
		if sensitive(in.rel) || isTraceArchive(in.src) {
			refused = append(refused, in.rel)
		}
	}
	if len(refused) > 0 && !allowSensitive {
		return c, fmt.Errorf("refusing files that may contain secrets or private data: %s; leave them out and publish only what should be shown (--allow-sensitive only if the user confirms they hold no secrets)", strings.Join(refused, ", "))
	}
	sort.Slice(c.inputs, func(i, j int) bool { return c.inputs[i].rel < c.inputs[j].rel })
	return c, nil
}

// copyInto copies the inputs below dst and records sizes, hashes and image
// dimensions. Sources may change meanwhile; the copy is what gets published.
func copyInto(dst string, inputs []input) ([]File, error) {
	files := make([]File, 0, len(inputs))
	for _, in := range inputs {
		target := filepath.Join(dst, filepath.FromSlash(in.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		f, err := copyFile(in.src, target)
		if err != nil {
			return nil, err
		}
		f.Path = in.rel
		files = append(files, f)
	}
	return files, nil
}

func copyFile(src, dst string) (File, error) {
	var f File
	in, err := os.Open(src)
	if err != nil {
		return f, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return f, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return f, fmt.Errorf("copy %s: %w", src, err)
	}
	f.Size, f.SHA256 = n, hex.EncodeToString(h.Sum(nil))
	if isImage(dst) {
		if r, err := os.Open(dst); err == nil {
			if cfg, _, err := image.DecodeConfig(r); err == nil {
				f.Width, f.Height = cfg.Width, cfg.Height
			}
			r.Close()
		}
	}
	return f, nil
}
