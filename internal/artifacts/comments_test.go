package artifacts

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publishShots(t *testing.T, s Store) {
	t.Helper()
	src := t.TempDir()
	write(t, filepath.Join(src, "a-dark-1440.png"), pngBytes(t, 200, 100, 30))
	write(t, filepath.Join(src, "notes.txt"), []byte("x"))
	if _, err := s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
}

func fp(v float64) *float64 { return &v }

func TestCommentsLifecycle(t *testing.T) {
	s, _ := testStore(t)
	publishShots(t, s)
	c1, err := s.AddComment("p", "n", "a-dark-1440.png", 1, fp(0.25), fp(0.5), "  Ring is clipped  ")
	if err != nil || c1.Number != 1 || c1.Text != "Ring is clipped" {
		t.Fatalf("%+v %v", c1, err)
	}
	c2, err := s.AddComment("p", "n", "a-dark-1440.png", 1, nil, nil, "Overall too busy")
	if err != nil || c2.Number != 2 || c2.X != nil {
		t.Fatalf("%+v %v", c2, err)
	}
	if c3, err := s.AddComment("p", "n", "notes.txt", 1, nil, nil, "typo"); err != nil || c3.Number != 1 {
		t.Fatalf("numbers count per file: %+v %v", c3, err)
	}
	for _, bad := range []struct {
		path    string
		version int
		x, y    *float64
		text    string
	}{
		{"missing.png", 1, nil, nil, "x"},
		{"a-dark-1440.png", 2, nil, nil, "x"},
		{"a-dark-1440.png", 1, fp(1.5), fp(0.1), "x"},
		{"a-dark-1440.png", 1, fp(0.5), nil, "x"},
		{"a-dark-1440.png", 1, nil, nil, "   "},
		{"a-dark-1440.png", 1, nil, nil, strings.Repeat("x", maxCommentRunes+1)},
		{"a-dark-1440.png", 1, nil, nil, "fine\x1b[1A\x1b[2K\rresolved"},
	} {
		if _, err := s.AddComment("p", "n", bad.path, bad.version, bad.x, bad.y, bad.text); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if err = s.Resolve("p", "n", []string{c1.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Resolve("p", "n", []string{"nope"}, true); err == nil {
		t.Fatal("resolved an unknown comment")
	}
	cs, err := s.Comments("p", "n")
	if err != nil || len(cs) != 3 || !cs[0].Resolved || cs[1].Resolved || OpenComments(cs) != 2 {
		t.Fatalf("%+v %v", cs, err)
	}
	// A torn last line from a crash does not hide the others.
	f, _ := os.OpenFile(s.commentsPath("p", "n"), os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(`{"type":"comment","id":"x`)
	f.Close()
	if cs, err = s.Comments("p", "n"); err != nil || len(cs) != 3 {
		t.Fatalf("torn line: %d %v", len(cs), err)
	}
	// The next comment starts on a new line instead of joining the torn one.
	if c4, err := s.AddComment("p", "n", "a-dark-1440.png", 1, nil, nil, "after\ta crash"); err != nil || c4.Text != "after\ta crash" || c4.Number != 3 {
		t.Fatalf("after a torn line: %+v %v", c4, err)
	}
	// Comments go with the artifact.
	if _, err = s.Unpublish([]string{"p/n"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Comments("p", "n"); err == nil {
		t.Fatal("comments outlived their artifact")
	}
}

func TestResolveAfterCommentLimit(t *testing.T) {
	s, _ := testStore(t)
	publishShots(t, s)
	c, err := s.AddComment("p", "n", "a-dark-1440.png", 1, nil, nil, "first")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(s.commentsPath("p", "n"), os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(strings.Repeat("{}\n", maxCommentsFile/3+1))
	f.Close()
	if _, err = s.AddComment("p", "n", "a-dark-1440.png", 1, nil, nil, "second"); err == nil {
		t.Fatal("accepted a comment beyond the limit")
	}
	if err = s.Resolve("p", "n", []string{c.ID}, true); err != nil {
		t.Fatalf("resolving beyond the comment limit: %v", err)
	}
}

func TestCommentHandler(t *testing.T) {
	s, _ := testStore(t)
	publishShots(t, s)
	h := CommentHandler(s)
	do := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://artifacts.dev.example.com"+path, strings.NewReader(body))
		r.Host = "artifacts.dev.example.com"
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	good := map[string]string{"Content-Type": "application/json", "Origin": "https://artifacts.dev.example.com", "Sec-Fetch-Site": "same-origin"}
	w := do("POST", "/p/n", `{"path":"a-dark-1440.png","version":1,"x":0.5,"y":0.5,"text":"hi"}`, good)
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var c Comment
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	for name, tc := range map[string]struct {
		method, path, body string
		headers            map[string]string
		code               int
	}{
		"form post":      {"POST", "/p/n", `{"path":"a-dark-1440.png","version":1,"text":"x"}`, map[string]string{"Content-Type": "text/plain", "Origin": "https://artifacts.dev.example.com"}, 403},
		"foreign origin": {"POST", "/p/n", `{"path":"a-dark-1440.png","version":1,"text":"x"}`, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.dev.example.com"}, 403},
		"no origin":      {"POST", "/p/n", `{"path":"a-dark-1440.png","version":1,"text":"x"}`, map[string]string{"Content-Type": "application/json"}, 403},
		"cross-site":     {"POST", "/p/n", `{}`, map[string]string{"Content-Type": "application/json", "Origin": "https://artifacts.dev.example.com", "Sec-Fetch-Site": "same-site"}, 403},
		"get":            {"GET", "/p/n", ``, good, 405},
		"bad label":      {"POST", "/P/n", `{}`, good, 404},
		"unknown":        {"POST", "/p/missing", `{"path":"a","version":1,"text":"x"}`, good, 400},
		"bad json":       {"POST", "/p/n", `{`, good, 400},
		"resolve":        {"POST", "/p/n/resolve", `{"ids":["` + c.ID + `"],"resolved":true}`, good, 204},
		"resolve empty":  {"POST", "/p/n/resolve", `{"ids":[]}`, good, 400},
		"other action":   {"POST", "/p/n/delete", `{}`, good, 404},
	} {
		if w := do(tc.method, tc.path, tc.body, tc.headers); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if cs, _ := s.Comments("p", "n"); len(cs) != 1 || !cs[0].Resolved {
		t.Fatalf("%+v", cs)
	}
}

func TestAnnotate(t *testing.T) {
	s, _ := testStore(t)
	publishShots(t, s)
	c, err := s.AddComment("p", "n", "a-dark-1440.png", 1, fp(0.25), fp(0.5), "here")
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Comments("p", "n")
	dir := t.TempDir()
	files, err := s.Annotate("p", "n", cs, dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	out := files["v1/a-dark-1440.png"]
	if out != filepath.Join(dir, "v1", "a-dark-1440.png") {
		t.Fatalf("written to %s", out)
	}
	data, _ := os.ReadFile(out)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// The pin is drawn at 25 % / 50 %; the digit's white strokes sit at its center.
	r, g, b, _ := img.At(50-10, 50).RGBA()
	if r>>8 != 0xc6 || g>>8 != 0x2f || b>>8 != 0x35 {
		t.Fatalf("no pin at the comment position: %x %x %x (%s)", r>>8, g>>8, b>>8, c.ID)
	}
	if r, _, _, _ := img.At(190, 10).RGBA(); r>>8 != 30 {
		t.Fatal("image outside the pin changed")
	}
}
