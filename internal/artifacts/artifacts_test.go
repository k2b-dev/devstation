package artifacts

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func testStore(t *testing.T) (Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	return Store{Root: filepath.Join(t.TempDir(), "artifacts"), Now: c.now}, c
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func pngBytes(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	img.Set(0, 0, color.Gray{Y: shade ^ 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseShot(t *testing.T) {
	cases := []struct {
		name  string
		row   string
		width int
		theme string
		ok    bool
	}{
		{"01-kanban-board-dark-1440.png", "01-kanban-board", 1440, "dark", true},
		{"01-kanban-board-dark-390-mobile-emulation.png", "01-kanban-board-mobile-emulation", 390, "dark", true},
		{"02-card-claimed-10plus-light-390.png", "02-card-claimed-10plus", 390, "light", true},
		{"10-drop-line-column-top-dark-1440-crop.png", "10-drop-line-column-top-crop", 1440, "dark", true},
		{"21-me-gruppen-light-1440-WITHOUT-gutter-control.png", "21-me-gruppen-WITHOUT-gutter-control", 1440, "light", true},
		{"01-busy-claimed-light-1440-blocker-hint-keyboard.png", "01-busy-claimed-blocker-hint-keyboard", 1440, "light", true},
		{"toggle-dark-mode-light-1440.png", "toggle-dark-mode", 1440, "light", true},
		{"liste-leer-dunkel-375.png", "liste-leer", 375, "dunkel", true},
		{"x-1440-dark.png", "x", 1440, "dark", true},
		{"hero-1280.png", "hero", 1280, "", true},
		{"30-dark-selected-card-contrast.png", "", 0, "", false},
		{"before.png", "", 0, "", false},
		{"shot-2026-09-27.png", "", 0, "", false},
	}
	for _, c := range cases {
		s, ok := parseShot(c.name)
		if ok != c.ok || (ok && (s.row != c.row || s.width != c.width || s.theme != c.theme)) {
			t.Errorf("%s: got %+v %v", c.name, s, ok)
		}
	}
}

func TestLayoutMatrix(t *testing.T) {
	names := []string{"10-b-dark-390.png", "2-a-light-1440.png", "2-a-dark-1440.png", "2-a-light-390.png", "2-a-light-390.html", "notes.txt", "30-dark-contrast.png", "sub/x-dark-1440.png", "plan.md", "embedded.png"}
	var files []File
	for _, n := range names {
		files = append(files, File{Path: n})
	}
	sections := layout(files, map[string]bool{"embedded.png": true, "plan.md": true})
	if len(sections) != 2 || sections[0].Dir != "" || sections[1].Dir != "sub" {
		t.Fatalf("sections: %+v", sections)
	}
	s := sections[0]
	var cols []string
	for _, c := range s.Columns {
		cols = append(cols, fmt.Sprintf("%d-%s", c.Width, c.Theme))
	}
	if strings.Join(cols, ",") != "1440-light,1440-dark,390-light,390-dark" {
		t.Fatalf("columns: %v", cols)
	}
	if len(s.Rows) != 2 || s.Rows[0].Label != "2-a" || s.Rows[1].Label != "10-b" {
		t.Fatalf("rows: %+v", s.Rows)
	}
	if !s.Rows[1].Cells[0].Missing || s.Rows[1].Cells[3].File.Path != "10-b-dark-390.png" {
		t.Fatalf("cells: %+v", s.Rows[1].Cells)
	}
	if a := s.Rows[0].Cells[2].Attach; len(a) != 1 || a[0].Path != "2-a-light-390.html" {
		t.Fatalf("attachment: %+v", a)
	}
	if len(s.Plain) != 1 || s.Plain[0].File.Path != "30-dark-contrast.png" || len(s.Other) != 1 || s.Other[0].Path != "notes.txt" {
		t.Fatalf("plain/other: %+v %+v", s.Plain, s.Other)
	}
}

func TestCollectFilters(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "shot-dark-1440.png"), []byte("png"))
	write(t, filepath.Join(dir, "api-token-dialog-dark-1440.png"), []byte("png"))
	write(t, filepath.Join(dir, ".env.local"), []byte("SECRET=1"))
	write(t, filepath.Join(dir, ".git", "config"), []byte("x"))
	write(t, filepath.Join(dir, "sub", "notes.md"), []byte("# hi"))
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	c, err := collect([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, in := range c.inputs {
		rels = append(rels, in.rel)
	}
	if strings.Join(rels, ",") != "api-token-dialog-dark-1440.png,shot-dark-1440.png,sub/notes.md" {
		t.Fatalf("inputs: %v", rels)
	}
	if strings.Join(c.skipped, ",") != ".env.local,.git,link" {
		t.Fatalf("skipped: %v", c.skipped)
	}
	secret := filepath.Join(t.TempDir(), "session-cookie.json")
	write(t, secret, []byte("{}"))
	if _, err = collect([]string{dir, secret}, false); err == nil || !strings.Contains(err.Error(), "session-cookie.json") {
		t.Fatalf("sensitive file accepted: %v", err)
	}
	if c, err = collect([]string{dir, secret}, true); err != nil || c.inputs[len(c.inputs)-1].rel != "session-cookie.json" {
		t.Fatalf("--allow-sensitive: %v", err)
	}
	for _, name := range []string{"app.env.local", "prod.env", "db.dump", "dump.sql", "dev.sqlite", "server.key", "cert.pem", "cert.p12", "trace.har", "SECRET.txt", "storageState.json", "e2e/storage-state.json", "auth.json", "results/trace.zip", "id_ed25519", "credentials.json", ".npmrc", "sub/.env", "secrets/db.json", "cookies/state.json", "session-token", "db.sqlite3", "dump.sql.gz", "terraform.tfstate.backup", "kubeconfig", "passwords.txt", "api_key.txt", "session.json", "vault.kdbx", "putty.ppk", "cloud.cookie"} {
		if !sensitive(name) {
			t.Errorf("%s not flagged", name)
		}
	}
	for _, name := range []string{"api-token-dialog-dark-1440.png", "plan.md", "session-timeout.md", "index.html", "tokens-dark.webm", "design-tokens.css", "cookie-banner.html", "credentials-form.html", "refresh-token-flow.md", "secrets-rotation-plan.md", "token.js"} {
		if sensitive(name) {
			t.Errorf("%s flagged", name)
		}
	}
	auth := filepath.Join(t.TempDir(), "playwright", ".auth")
	write(t, filepath.Join(auth, "user.json"), []byte("{}"))
	for _, arg := range []string{auth, filepath.Join(auth, "user.json")} {
		if _, err = collect([]string{arg}, false); err == nil {
			t.Fatalf("accepted hidden argument %s", arg)
		}
	}
	report := t.TempDir()
	write(t, filepath.Join(report, "index.html"), []byte("<p>report</p>"))
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("trace.network")
	_, _ = w.Write([]byte("Cookie: x"))
	_ = zw.Close()
	write(t, filepath.Join(report, "data", "3f2a.zip"), zipped.Bytes())
	if _, err = collect([]string{report}, false); err == nil || !strings.Contains(err.Error(), "data/3f2a.zip") {
		t.Fatalf("accepted a trace archive: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "b\xe4r.png")
	write(t, bad, []byte("png"))
	if _, err = collect([]string{bad}, false); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("accepted a non-UTF-8 name: %v", err)
	}
	other := t.TempDir()
	write(t, filepath.Join(other, "shot-dark-1440.png"), []byte("png"))
	if _, err = collect([]string{filepath.Join(dir, "shot-dark-1440.png"), filepath.Join(other, "shot-dark-1440.png")}, false); err == nil {
		t.Fatal("accepted two inputs with the same destination")
	}
	if _, err = collect([]string{filepath.Join(dir, "missing")}, false); err == nil {
		t.Fatal("accepted a missing path")
	}
}

func TestPublishVersions(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "01-board-dark-1440.png"), pngBytes(t, 40, 30, 10))
	write(t, filepath.Join(src, "01-board-light-1440.png"), pngBytes(t, 40, 30, 200))
	r, err := s.Publish(Options{Project: "cloud", Name: "night", Title: "Night check", Link: "https://example.com/pr/1", Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version.N != 1 || r.Version.Kind != "gallery" || r.Version.Files[0].Width != 40 {
		t.Fatalf("v1: %+v", r.Version)
	}
	v1 := read(t, filepath.Join(s.versionDir("cloud", "night", 1), "01-board-dark-1440.png"))
	write(t, filepath.Join(src, "01-board-dark-1440.png"), pngBytes(t, 40, 30, 90))
	write(t, filepath.Join(src, "02-new-dark-1440.png"), pngBytes(t, 40, 30, 90))
	r, err = s.Publish(Options{Project: "cloud", Name: "night", Keep: true, Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	m := r.Meta
	if r.Version.N != 2 || m.Title != "Night check" || m.Link != "https://example.com/pr/1" || !m.Keep {
		t.Fatalf("sticky metadata: %+v", m)
	}
	if read(t, filepath.Join(s.versionDir("cloud", "night", 1), "01-board-dark-1440.png")) != v1 {
		t.Fatal("version 1 changed")
	}
	current := read(t, filepath.Join(s.artifact("cloud", "night"), "index.html"))
	for _, want := range []string{`src="v/2/01-board-dark-1440.png"`, `href="./compare/1-2/"`, `id="02-new-dark-1440.png"`, "Night check", `id="lightbox"`, `data-row="01-board"`, `data-col="dark · 1440"`, "showModal"} {
		if !strings.Contains(current, want) {
			t.Fatalf("current page lacks %s", want)
		}
	}
	page := read(t, filepath.Join(s.versionDir("cloud", "night", 1), "index.html"))
	if !strings.Contains(page, `src="./01-board-dark-1440.png"`) || !strings.Contains(page, `href="../../">latest version`) {
		t.Fatal("version page links are not relative to the version or lack the latest link")
	}
	if strings.Contains(page, "· latest") || !strings.Contains(current, "v2 · latest") {
		t.Fatal("only the stable page may claim to be the latest version")
	}
	compare := read(t, filepath.Join(s.artifact("cloud", "night"), "compare", "1-2", "index.html"))
	for _, want := range []string{"1 changed", "1 added", "1 unchanged", `class="slider"`, `src="../../v/1/01-board-dark-1440.png"`} {
		if !strings.Contains(compare, want) {
			t.Fatalf("compare page lacks %s", want)
		}
	}
	for _, p := range []string{filepath.Join(s.Site(), "index.html"), filepath.Join(s.project("cloud"), "index.html")} {
		if !strings.Contains(read(t, p), "cloud") {
			t.Fatalf("%s misses the project", p)
		}
	}
	var saved Meta
	if err = json.Unmarshal([]byte(read(t, s.metaPath("cloud", "night"))), &saved); err != nil || len(saved.Versions) != 2 {
		t.Fatalf("metadata: %v", err)
	}
	if strings.Contains(read(t, s.metaPath("cloud", "night")), src) {
		t.Fatal("metadata contains the source path")
	}
	if entries, _ := os.ReadDir(filepath.Join(s.Root, "tmp")); len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}
}

func TestSiteMode(t *testing.T) {
	s, _ := testStore(t)
	mock := t.TempDir()
	write(t, filepath.Join(mock, "index.html"), []byte("<script>app()</script>"))
	write(t, filepath.Join(mock, "app.js"), []byte("function app(){}"))
	r, err := s.Publish(Options{Project: "wf", Name: "mockup", Paths: []string{mock}})
	if err != nil || r.Version.Kind != "site" {
		t.Fatalf("%v %+v", err, r.Version)
	}
	if read(t, filepath.Join(s.versionDir("wf", "mockup", 1), "index.html")) != "<script>app()</script>" {
		t.Fatal("site index was modified")
	}
	if cur := read(t, filepath.Join(s.artifact("wf", "mockup"), "index.html")); !strings.Contains(cur, `location.replace("./v/1/"`) || strings.Contains(cur, "lightbox") {
		t.Fatalf("stable page does not redirect: %s", cur)
	}
	single := filepath.Join(t.TempDir(), "bilder zuordnung.html")
	write(t, single, []byte("<p>mock</p>"))
	if r, err = s.Publish(Options{Project: "wf", Name: "single", Paths: []string{single}}); err != nil || r.Version.Entry != "bilder zuordnung.html" {
		t.Fatalf("%v %+v", err, r.Version)
	}
	if cur := read(t, filepath.Join(s.artifact("wf", "single"), "index.html")); !strings.Contains(cur, "./v/1/bilder%20zuordnung.html") {
		t.Fatalf("single file redirect: %s", cur)
	}
	if v := read(t, filepath.Join(s.versionDir("wf", "single", 1), "index.html")); !strings.Contains(v, "./bilder%20zuordnung.html") {
		t.Fatalf("version index redirect: %s", v)
	}
}

func TestMarkdownPlan(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	plan := "# Plan für „Slice“\n\n| A | B |\n|---|---|\n| ä | ~~alt~~ |\n\n- [x] done\n- [ ] open\n\n```\n+------+\n| ASCII mockup that must not wrap at all ever |\n+------+\n```\n\n![shot](img/a-dark-1440.png) [other](notes.md#details) [missing](nope.png) [ext](https://example.com) [bad](javascript:alert(1))\n\n<script>alert(1)</script>\n"
	write(t, filepath.Join(src, "plan.md"), []byte(plan))
	write(t, filepath.Join(src, "notes.md"), []byte("## Details\n"))
	write(t, filepath.Join(src, "img", "a-dark-1440.png"), pngBytes(t, 10, 10, 1))
	write(t, filepath.Join(src, "img", "b-dark-1440.png"), pngBytes(t, 10, 10, 2))
	r, err := s.Publish(Options{Project: "cloud", Name: "plan", Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version.Kind != "plan" || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "nope.png") {
		t.Fatalf("%+v %v", r.Version.Kind, r.Warnings)
	}
	page := read(t, filepath.Join(s.artifact("cloud", "plan"), "index.html"))
	for _, want := range []string{"<table>", "<del>alt</del>", `type="checkbox"`, "„Slice“", `src="v/1/img/a-dark-1440.png"`, `href="#details"`, `href="https://example.com"`, "ASCII mockup", `id="img/b-dark-1440.png"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("plan page lacks %s", want)
		}
	}
	for _, bad := range []string{"<script>alert", "javascript:alert", `id="img/a-dark-1440.png"`} {
		if strings.Contains(page, bad) {
			t.Fatalf("plan page contains %s", bad)
		}
	}
	// Natural order: notes.md before plan.md; neither is index.md or README.md.
	if i, j := strings.Index(page, `id="notes.md"`), strings.Index(page, `id="plan.md"`); i < 0 || j < 0 || i > j {
		t.Fatal("documents missing or not in natural order")
	}
}

func TestHostileNames(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	for _, n := range []string{"javascript:alert(1)-dark-1440.png", `<img src=x onerror=alert(1)>-dark-390.png`, "a?b#c%-light-1440.png", `quote"s-light-390.png`} {
		write(t, filepath.Join(src, n), pngBytes(t, 4, 4, 3))
	}
	if _, err := s.Publish(Options{Project: "x", Name: "y", Title: `<script>alert(1)</script>`, Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(s.artifact("x", "y"), "index.html"), filepath.Join(s.versionDir("x", "y", 1), "index.html")} {
		page := read(t, p)
		if strings.Contains(page, "<script>alert") || strings.Contains(page, "<img src=x") {
			t.Fatalf("unescaped name or title in %s", p)
		}
		for _, m := range regexp.MustCompile(`(?:href|src)="([^"]*)"`).FindAllStringSubmatch(page, -1) {
			u := m[1]
			if !(strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") || strings.HasPrefix(u, "v/") || strings.HasPrefix(u, "#")) {
				t.Fatalf("unexpected URL %q in %s", u, p)
			}
		}
		if !strings.Contains(page, "a%3Fb%23c%25-light-1440.png") {
			t.Fatalf("special characters not escaped in %s", p)
		}
	}
	for _, bad := range []Options{{Project: "x", Name: "y", Link: "javascript:alert(1)"}, {Project: "x", Name: "y", Title: "a\nb"}, {Project: "X", Name: "y"}, {Project: "x", Name: "a.b"}} {
		bad.Paths = []string{src}
		if _, err := s.Publish(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestExpiryKeepAndUnpublish(t *testing.T) {
	s, clk := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "a-dark-1440.png"), pngBytes(t, 4, 4, 1))
	for _, n := range []string{"old", "kept", "renewed"} {
		if _, err := s.Publish(Options{Project: "p", Name: n, Keep: n == "kept", Paths: []string{src}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Publish(Options{Project: "q", Name: "gone", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	clk.add(Lifetime + time.Minute)
	r, err := s.Publish(Options{Project: "p", Name: "renewed", Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.Removed, ",") != "p/old,q/gone" || r.Version.N != 2 {
		t.Fatalf("removed %v, version %d", r.Removed, r.Version.N)
	}
	if _, err = os.Stat(s.project("q")); !os.IsNotExist(err) {
		t.Fatal("empty project not removed")
	}
	if strings.Contains(read(t, filepath.Join(s.Site(), "index.html")), `href="./q/"`) {
		t.Fatal("root index still lists the removed project")
	}
	list, _, _ := s.List("")
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if _, err = s.Keep("p", "renewed"); err != nil {
		t.Fatal(err)
	}
	clk.add(10 * Lifetime)
	if removed, err := s.Unpublish(nil); err != nil || len(removed) != 0 {
		t.Fatalf("kept artifacts were pruned: %v %v", removed, err)
	}
	if _, err = s.Unpublish([]string{"p/missing"}); err == nil {
		t.Fatal("unpublished an unknown artifact")
	}
	if removed, err := s.Unpublish([]string{"p/kept", "p/renewed"}); err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", removed, err)
	}
	if list, _, _ = s.List(""); len(list) != 0 {
		t.Fatalf("left: %+v", list)
	}
	if _, err = os.Stat(s.project("p")); !os.IsNotExist(err) {
		t.Fatal("project not removed after its last artifact")
	}
}

func TestInterruptedPublishLeftovers(t *testing.T) {
	s, clk := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "a.png"), pngBytes(t, 4, 4, 1))
	if _, err := s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	// A crash after the rename but before metadata leaves v/2 behind.
	if err := os.MkdirAll(s.versionDir("p", "n", 2), 0700); err != nil {
		t.Fatal(err)
	}
	// A crashed first publish leaves an artifact directory without metadata.
	if err := os.MkdirAll(s.versionDir("p", "orphan", 1), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}})
	if err != nil || r.Version.N != 3 {
		t.Fatalf("%v %d", err, r.Version.N)
	}
	if _, err = os.Stat(s.artifact("p", "orphan")); err != nil {
		t.Fatal("fresh orphan removed too early")
	}
	old := clk.now().Add(-2 * staleTemp)
	_ = os.Chtimes(s.artifact("p", "orphan"), old, old)
	if _, err = s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.artifact("p", "orphan")); !os.IsNotExist(err) {
		t.Fatal("stale orphan not removed")
	}
}

func TestConcurrentPublish(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(src, fmt.Sprintf("%02d-shot-dark-1440.png", i)), pngBytes(t, 8, 8, uint8(i)))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("n%d", i)
		if i >= 6 {
			name = "same"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Publish(Options{Project: "p", Name: name, Paths: []string{src}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	list, _, err := s.List("p")
	if err != nil || len(list) != 7 {
		t.Fatalf("%v %d", err, len(list))
	}
	m, err := s.readMeta("p", "same")
	if err != nil || len(m.Versions) != 2 || m.Versions[1].N != 2 {
		t.Fatalf("same name: %v %+v", err, m.Versions)
	}
	index := read(t, filepath.Join(s.project("p"), "index.html"))
	for i := 0; i < 6; i++ {
		if !strings.Contains(index, fmt.Sprintf(`href="./n%d/"`, i)) {
			t.Fatalf("project index misses n%d", i)
		}
	}
}

func TestHeadingIDs(t *testing.T) {
	published := map[string]bool{"a.md": true, "b.md": true}
	out, _, _ := renderDocs([]parsedDocInput{
		{"a.md", []byte("# Größe für #322\n\n## Files\n\n## Overview\n\n[b](b.md#überblick) [b-over](b.md#overview) [own](#overview)\n")},
		{"b.md", []byte("## Überblick\n\n## Größe für #322\n\n## Overview\n\n## Overview\n\n[own](#overview) [second](#overview-1) [a](a.md)\n")},
	}, "./", published)
	a, b := out[0], out[1]
	for _, want := range []string{`id="größe-für-322"`, `id="files-1"`, `id="overview"`, `href="#%C3%BCberblick"`, `href="#overview-1">b-over`, `href="#overview">own`} {
		if !strings.Contains(a, want) {
			t.Fatalf("a lacks %s: %s", want, a)
		}
	}
	for _, want := range []string{`id="überblick"`, `id="größe-für-322-1"`, `id="overview-1"`, `id="overview-1-1"`, `href="#overview-1">own`, `href="#overview-1-1">second`, `href="#a.md">a`} {
		if !strings.Contains(b, want) {
			t.Fatalf("b lacks %s: %s", want, b)
		}
	}
}

func TestHeadingTextRules(t *testing.T) {
	out, _, _ := renderDocs([]parsedDocInput{{"a.md", []byte("## _Optional_ steps\n\n## Title <sup>beta</sup>\n\n## API [reference](https://example.com)\n\n## Setup\n\n## Setup\n\n## Setup-1\n")}}, "./", map[string]bool{"a.md": true})
	for _, want := range []string{`id="optional-steps"`, `id="title-beta"`, `id="api-reference"`, `id="setup"`, `id="setup-1"`, `id="setup-1-1"`} {
		if !strings.Contains(out[0], want) {
			t.Fatalf("lacks %s: %s", want, out[0])
		}
	}
}

func TestRenderedDocsCaps(t *testing.T) {
	files := []File{{Path: "b.md", Size: 3 << 20}, {Path: "c.md", Size: 2 << 20}, {Path: "d.md", Size: 2 << 20}, {Path: "e.md", Size: 2 << 20}, {Path: "f.md", Size: 2 << 20}, {Path: "README.md", Size: 1}}
	var got []string
	for _, d := range renderedDocs(files) {
		got = append(got, d.Path)
	}
	if strings.Join(got, ",") != "README.md,c.md,d.md,e.md" {
		t.Fatalf("rendered: %v", got)
	}
}

func TestLargeMarkdownIsListed(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "log.md"), bytes.Repeat([]byte("line\n"), maxMarkdown/5+1))
	r, err := s.Publish(Options{Project: "p", Name: "log", Paths: []string{src}})
	if err != nil || r.Version.Kind != "files" {
		t.Fatalf("%v %+v", err, r.Version.Kind)
	}
	if page := read(t, filepath.Join(s.artifact("p", "log"), "index.html")); !strings.Contains(page, `href="v/1/log.md"`) || strings.Contains(page, `class="doc"`) {
		t.Fatal("large Markdown was rendered instead of listed")
	}
}

func TestUnpublishDuplicatesAndDamagedMetadata(t *testing.T) {
	s, clk := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "a.png"), pngBytes(t, 4, 4, 1))
	for _, n := range []string{"a", "b", "old"} {
		if _, err := s.Publish(Options{Project: "p", Name: n, Paths: []string{src}}); err != nil {
			t.Fatal(err)
		}
	}
	clk.add(Lifetime / 2)
	if _, err := s.Publish(Options{Project: "p", Name: "b", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	clk.add(Lifetime/2 + time.Minute)
	removed, err := s.Unpublish([]string{"p/a", "p/a"})
	if err != nil || strings.Join(removed, ",") != "p/a,p/old" {
		t.Fatalf("%v %v", removed, err)
	}
	if strings.Contains(read(t, filepath.Join(s.project("p"), "index.html")), `href="./a/"`) {
		t.Fatal("project index still links the removed artifact")
	}
	write(t, s.metaPath("p", "b"), []byte("{broken"))
	if _, err = s.Publish(Options{Project: "q", Name: "c", Paths: []string{src}}); err != nil {
		t.Fatalf("damaged metadata blocked another publish: %v", err)
	}
	if list, damaged, err := s.List(""); err != nil || len(list) != 1 || strings.Join(damaged, ",") != "p/b" {
		t.Fatalf("list: %v %v %v", list, damaged, err)
	}
	// Removing the last valid artifact of a project keeps the damaged one.
	if _, err = s.Publish(Options{Project: "p", Name: "d", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Unpublish([]string{"p/d"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.artifact("p", "b")); err != nil {
		t.Fatal("damaged artifact removed together with the last valid one")
	}
	if _, err = s.Unpublish([]string{"p/b"}); err != nil {
		t.Fatalf("could not remove the damaged artifact: %v", err)
	}
	if _, err = os.Stat(s.project("p")); !os.IsNotExist(err) {
		t.Fatal("empty project not removed")
	}
}

func TestCleanupDuringDiscard(t *testing.T) {
	s, _ := testStore(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				s.cleanup()
			}
		}
	}()
	for i := 0; i < 500; i++ {
		dir := filepath.Join(s.Site(), fmt.Sprint(i))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := s.discard(dir); err != nil {
			close(stop)
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
}

func TestNaturalLess(t *testing.T) {
	if !naturalLess("2-a", "10-a") || naturalLess("10-a", "2-a") || !naturalLess("a", "b") || !naturalLess("a", "a1") {
		t.Fatal("natural order")
	}
}
