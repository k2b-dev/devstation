package shot

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	cases := []struct{ arg, label, url string }{
		{"https://dev.example.com/app/spaces/X?view=kanban", "app-spaces-x", "https://dev.example.com/app/spaces/X?view=kanban"},
		{"board=https://dev.example.com/app", "board", "https://dev.example.com/app"},
		{"https://app.dev.example.com/", "app", "https://app.dev.example.com/"},
		{"Liste Leer=http://127.0.0.1:3000/a=b", "liste-leer", "http://127.0.0.1:3000/a=b"},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.arg)
		if err != nil || got.Label != c.label || got.URL != c.url {
			t.Errorf("%s: %+v %v", c.arg, got, err)
		}
	}
	for _, bad := range []string{"ftp://x/y", "x=notaurl", "/relative", "===https://x/"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestFindBrowserExplicit(t *testing.T) {
	if _, err := FindBrowser("/does/not/exist"); err == nil {
		t.Fatal("accepted a missing browser")
	}
	fake := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVSTATION_BROWSER", fake)
	if got, err := FindBrowser(""); err != nil || got != fake {
		t.Fatalf("%s %v", got, err)
	}
}

func TestOptionValidation(t *testing.T) {
	base := Options{Targets: []Target{{"a", "https://x.example/"}}, Out: "out", Themes: []string{"light"}, Widths: []int{1440}}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Options){
		"theme":     func(o *Options) { o.Themes = []string{"sepia"} },
		"no theme":  func(o *Options) { o.Themes = nil },
		"width":     func(o *Options) { o.Widths = []int{50} },
		"scale":     func(o *Options) { o.Scale = 5 },
		"out":       func(o *Options) { o.Out = "" },
		"duplicate": func(o *Options) { o.Targets = append(o.Targets, Target{"a", "https://x.example/?b"}) },
	} {
		o := base
		mutate(&o)
		if err := o.validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, o)
		}
	}
	if _, err := ParseTarget("https://user:pw@x.example/"); err == nil || strings.Contains(err.Error(), "pw") {
		t.Fatalf("credentials in URL: %v", err)
	}
}

// pixel returns the color at x,y of a PNG as "#rrggbb".
func pixel(t *testing.T, path string, x, y int) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(x, y).RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func size(t *testing.T, path string) image.Point {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return image.Pt(cfg.Width, cfg.Height)
}

func TestShotIntegration(t *testing.T) {
	if os.Getenv("DEVSTATION_INTEGRATION") != "1" {
		t.Skip("set DEVSTATION_INTEGRATION=1 with Chrome or Chromium installed")
	}
	if _, err := FindBrowser(""); err != nil {
		t.Fatal(err)
	}
	selectorWait = 2 * time.Second
	ctx := context.Background()
	mux := http.NewServeMux()
	page := func(body string) string {
		return `<!doctype html><meta name="viewport" content="width=device-width"><style>html,body{margin:0}body{background:#ffffff;height:300px}@media (prefers-color-scheme:dark){body{background:#000000}}</style>` + body
	}
	mux.HandleFunc("/scheme", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, page(`<p>scheme</p>`))
	})
	mux.HandleFunc("/cookie-theme", func(w http.ResponseWriter, r *http.Request) {
		color := "#00ff00"
		if c, err := r.Cookie("theme"); err == nil && c.Value == "dark" {
			color = "#0000ff"
		}
		fmt.Fprintf(w, `<!doctype html><style>body{margin:0;background:%s}</style><p>theme</p>`, color)
	})
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err != nil || c.Value != "s3cret" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, page(`<button id="b" style="width:200px;height:100px" onclick="document.body.style.background='#ff0000'">go</button>`))
	})
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/scheme", http.StatusFound) })
	mux.HandleFunc("/long", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><style>body{margin:0}</style><div style="height:3000px;background:#123456"></div>`)
	})
	mux.HandleFunc("/slow-api", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2500 * time.Millisecond)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/spa", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#ffffff"><script>fetch("/slow-api").then(() => document.body.style.background = "#00aa00")</script>`)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#00aa00"><script>new EventSource("/events")</script>`)
	})
	mux.HandleFunc("/guard", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><script>location.replace("/scheme")</script>`)
	})
	mux.HandleFunc("/alert", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#00aa00"><script>alert("hi")</script>`)
	})
	mux.HandleFunc("/remember", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0"><script>
if (!localStorage.t) localStorage.t = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
document.body.style.background = localStorage.t === "dark" ? "#000000" : "#ffffff";</script>`)
	})
	mux.HandleFunc("/pointer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0"><script>
document.body.style.background = matchMedia("(hover: hover)").matches ? "#ff0000" : "#0000ff";</script>`)
	})
	mux.HandleFunc("/hidden", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><dialog><button id="x">x</button></dialog>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	out := t.TempDir()
	cookieless := Options{Out: out, Themes: []string{"light", "dunkel"}, Widths: []int{800, 390}}

	o := cookieless
	o.Targets = []Target{{"scheme", srv.URL + "/scheme"}, {"theme", srv.URL + "/cookie-theme"}}
	o.ThemeCookie = "theme"
	r, err := Run(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 8 || r.Files[0].Status != 200 {
		t.Fatalf("files: %+v", r.Files)
	}
	for file, want := range map[string]string{
		"scheme-light-800.png": "#ffffff", "scheme-dunkel-800.png": "#000000", "scheme-dunkel-390.png": "#000000",
		"theme-light-800.png": "#00ff00", "theme-dunkel-390.png": "#0000ff",
	} {
		if got := pixel(t, filepath.Join(out, file), 5, 250); got != want {
			t.Errorf("%s: %s, want %s", file, got, want)
		}
	}
	if s := size(t, filepath.Join(out, "scheme-light-390.png")); s != image.Pt(390, 844) {
		t.Errorf("phone viewport: %v", s)
	}

	o = cookieless
	o.Targets = []Target{{"private", srv.URL + "/private"}}
	o.Themes, o.Widths = []string{"light"}, []int{800}
	if _, err = Run(ctx, o); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected HTTP 403: %v", err)
	}
	o.Cookies = []Cookie{{"session", "s3cret"}}
	o.Clicks = []string{"#b"}
	if _, err = Run(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, filepath.Join(out, "private-light-800.png"), 700, 250); got != "#ff0000" {
		t.Errorf("click had no effect: %s", got)
	}
	o.Clicks, o.WaitFor = nil, "#missing"
	began := time.Now()
	if _, err = Run(ctx, o); err == nil || !strings.Contains(err.Error(), "no visible element") || time.Since(began) > 10*time.Second {
		t.Fatalf("expected a quick missing selector error: %v after %s", err, time.Since(began))
	}

	o = cookieless
	o.Targets = []Target{{"old", srv.URL + "/old"}}
	o.Themes, o.Widths = []string{"light"}, []int{800}
	if r, err = Run(ctx, o); err != nil || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "instead") {
		t.Fatalf("expected a redirect warning: %v %v", r.Warnings, err)
	}

	o.Targets, o.FullPage = []Target{{"long", srv.URL + "/long"}}, true
	if _, err = Run(ctx, o); err != nil {
		t.Fatal(err)
	}
	if s := size(t, filepath.Join(out, "long-light-800.png")); s != image.Pt(800, 3000) {
		t.Errorf("full page: %v", s)
	}

	one := Options{Out: out, Themes: []string{"light"}, Widths: []int{800}}
	one.Targets = []Target{{"spa", srv.URL + "/spa"}, {"live", srv.URL + "/live"}, {"alert", srv.URL + "/alert"}}
	began = time.Now()
	if r, err = Run(ctx, one); err != nil {
		t.Fatal(err)
	}
	if got := pixel(t, filepath.Join(out, "spa-light-800.png"), 5, 5); got != "#00aa00" {
		t.Errorf("captured before the slow request finished: %s", got)
	}
	if time.Since(began) > 15*time.Second {
		t.Errorf("a live event stream held up the capture: %s", time.Since(began))
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "dismissed the alert dialog") {
		t.Errorf("dialog warning: %v", r.Warnings)
	}

	one.Targets = []Target{{"guard", srv.URL + "/guard"}}
	if r, err = Run(ctx, one); err != nil || len(r.Warnings) != 1 || !strings.Contains(r.Files[0].FinalURL, "/scheme") {
		t.Fatalf("redirect by script before load: %+v %v %v", r.Files, r.Warnings, err)
	}

	o = cookieless
	o.Targets, o.Widths = []Target{{"remember", srv.URL + "/remember"}, {"pointer", srv.URL + "/pointer"}}, []int{800, 390}
	if _, err = Run(ctx, o); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"remember-light-800.png": "#ffffff", "remember-dunkel-800.png": "#000000",
		"pointer-light-800.png": "#ff0000", "pointer-light-390.png": "#0000ff",
	} {
		if got := pixel(t, filepath.Join(out, file), 5, 5); got != want {
			t.Errorf("%s: %s, want %s", file, got, want)
		}
	}

	one.Targets, one.Clicks = []Target{{"hidden", srv.URL + "/hidden"}}, []string{"#x"}
	if _, err = Run(ctx, one); err == nil || !strings.Contains(err.Error(), "no visible element") {
		t.Fatalf("clicked a hidden element: %v", err)
	}
}
