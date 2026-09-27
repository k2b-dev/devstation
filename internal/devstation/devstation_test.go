package devstation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k2b-dev/devstation/internal/artifacts"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	// macOS test temp directories otherwise exceed Unix socket path limits.
	dir, err := os.MkdirTemp("/tmp", "ds-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return Config{Domain: "dev.example.com", Listen: "127.0.0.1:8443", Certificate: "/tmp/cert.pem", Key: "/tmp/key.pem", dir: dir}
}
func TestFailedReloadRestoresState(t *testing.T) {
	c := testConfig(t)
	old, _ := render(c, []Route{{Name: "first", Port: 3000}})
	if err := atomicWrite(c.statePath(), old); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	err := apply(c, []Route{{Name: "second", Port: 4000}}, false, func(args ...string) error {
		if args[0] == "reload" {
			reloads++
			if reloads == 1 {
				return errors.New("rejected")
			}
		}
		return nil
	})
	if err == nil || reloads != 2 {
		t.Fatalf("expected failure and reconciliation: %v, %d", err, reloads)
	}
	got, _ := os.ReadFile(c.statePath())
	if !bytes.Equal(got, old) {
		t.Fatal("persisted state was not restored")
	}
}
func TestValidationFailureLeavesState(t *testing.T) {
	c := testConfig(t)
	old, _ := render(c, []Route{{Name: "first", Port: 3000}})
	_ = atomicWrite(c.statePath(), old)
	err := apply(c, []Route{{Name: "second", Port: 4000}}, false, func(...string) error { return errors.New("invalid cert") })
	got, _ := os.ReadFile(c.statePath())
	if err == nil || !bytes.Equal(old, got) {
		t.Fatal("validation modified state")
	}
}
func TestLockRejectsConcurrentWriter(t *testing.T) {
	c := testConfig(t)
	unlock, err := lock(c)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if second, err := lock(c); err == nil {
		second()
		t.Fatal("acquired concurrent lock")
	}
}
func TestConfigAndInputValidation(t *testing.T) {
	c := testConfig(t)
	p := filepath.Join(c.dir, "config.toml")
	valid := "domain = 'dev.example.com'\ncertificate = '/tmp/cert'\nkey = '/tmp/key'\n"
	for _, extra := range []string{"unknown = true\n", "listen = ':443'\n"} {
		_ = os.WriteFile(p, []byte(valid+extra), 0600)
		if _, err := LoadConfig(p); err == nil {
			t.Fatalf("accepted %s", extra)
		}
	}
	_ = os.WriteFile(p, []byte(valid), 0600)
	for _, args := range [][]string{{"expose", "0", "--name", "ok"}, {"expose", "3000", "--name", "../../bad"}, {"expose", "3000", "--name", "a.b"}, {"expose", "3000", "--name", "-bad"}, {"serve", "/does-not-exist", "--name", "ok"}, {"serve", "/tmp", "--name", "a.b"}, {"unexpose", "missing"}} {
		if err := Run(append([]string{"--config", p}, args...), "test", &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestRoutesRoundTrip(t *testing.T) {
	c := testConfig(t)
	err := apply(c, []Route{{Name: "z", Port: 4000}, {Name: "a", Port: 3000}}, true, func(...string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	routes, err := readRoutes(c.statePath())
	if err != nil || len(routes) != 2 || routes[0].Name != "a" {
		t.Fatalf("%v %v", routes, err)
	}
	data, _ := os.ReadFile(c.statePath())
	if strings.Contains(string(data), "0.0.0.0") {
		t.Fatal("unexpected public listener")
	}
}

func TestStaticRoutesRoundTrip(t *testing.T) {
	c := testConfig(t)
	routes := []Route{
		{Name: "docs", Kind: "directory", Path: "/srv/site"},
		{Name: "single", Kind: "file", Path: "/srv/page.html"},
		{Name: "app", Port: 3000},
	}
	if err := apply(c, routes, true, func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := readRoutes(c.statePath())
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Name != "app" || got[0].Port != 3000 || got[1].Kind != "directory" || got[1].Path != "/srv/site" || got[2].Kind != "file" || got[2].Path != "/srv/page.html" {
		t.Fatalf("wrong static routes: %+v", got)
	}
}

func TestListShowsSavedURLsAfterConfigEdit(t *testing.T) {
	c := testConfig(t)
	configPath := filepath.Join(c.dir, "config.toml")
	changed := "domain = 'new.example.com'\nlisten = '127.0.0.1:9443'\ncertificate = '/tmp/cert'\nkey = '/tmp/key'\n"
	_ = os.WriteFile(configPath, []byte(changed), 0600)
	old := c
	old.dir = filepath.Join(c.dir, "state")
	_ = os.Mkdir(old.dir, 0700)
	data, _ := render(old, []Route{{Name: "example", Port: 3000}})
	_ = atomicWrite(old.statePath(), data)
	var output bytes.Buffer
	if err := Run([]string{"--config", configPath, "list", "--json"}, "test", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "https://example.dev.example.com:8443") {
		t.Fatal(output.String())
	}
}

func TestArtifactsRouteRoundTrip(t *testing.T) {
	c := testConfig(t)
	site := filepath.Join(c.dir, "artifacts", "site")
	if err := ensureArtifactsRoute(c, site, func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	routes, err := readRoutes(c.statePath())
	if err != nil || len(routes) != 1 || routes[0].Name != "artifacts" || routes[0].Kind != "artifacts" || routes[0].Path != site {
		t.Fatalf("%+v %v", routes, err)
	}
	data, _ := os.ReadFile(c.statePath())
	if !strings.Contains(string(data), `"Cache-Control": [`) || !strings.Contains(string(data), `"no-cache"`) {
		t.Fatalf("missing cache header: %s", data)
	}
	calls := 0
	if err = ensureArtifactsRoute(c, site, func(...string) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("existing route changed again: %v %d", err, calls)
	}
	if err = ensureArtifactsRoute(c, filepath.Join(c.dir, "elsewhere"), func(...string) error { return nil }); err == nil {
		t.Fatal("replaced an artifacts route with another root")
	}
	// A header set saved by an older version still reads as the artifacts route.
	older := strings.Replace(string(data), `"no-cache"`, `"max-age=600"`, 1)
	_ = os.WriteFile(c.statePath(), []byte(older), 0600)
	if routes, err = readRoutes(c.statePath()); err != nil || routes[0].Kind != "artifacts" {
		t.Fatalf("older header set rejected: %v", err)
	}
	smuggled := strings.Replace(string(data), `"handler": "headers",`, `"handler": "headers", "root": "/", `, 1)
	_ = os.WriteFile(c.statePath(), []byte(smuggled), 0600)
	if _, err = readRoutes(c.statePath()); err == nil {
		t.Fatal("accepted a headers handler with extra fields")
	}
}

func TestArtifactsRouteNameConflict(t *testing.T) {
	c := testConfig(t)
	if err := apply(c, []Route{{Name: "artifacts", Port: 3000}}, true, func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	err := ensureArtifactsRoute(c, "/tmp/site", func(...string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "dev unexpose artifacts") {
		t.Fatalf("expected a conflict: %v", err)
	}
}

// fakeCaddy puts a caddy that accepts every command first on PATH.
func fakeCaddy(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "caddy"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestArtifactCommands(t *testing.T) {
	fakeCaddy(t)
	c := testConfig(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(c.dir, "data"))
	configPath := filepath.Join(c.dir, "config.toml")
	_ = os.WriteFile(configPath, []byte("domain = 'dev.example.com'\ncertificate = '/tmp/cert'\nkey = '/tmp/key'\n"), 0600)
	shots := filepath.Join(c.dir, "shots")
	_ = os.MkdirAll(shots, 0700)
	_ = os.WriteFile(filepath.Join(shots, "a-dark-1440.png"), []byte("png"), 0600)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := Run(append([]string{"--config", configPath}, args...), "test", &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	var published struct {
		URL     string `json:"url"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal([]byte(run("publish", shots, "--project", "cloud", "--name", "board", "--json")), &published); err != nil || published.URL != "https://artifacts.dev.example.com:8443/cloud/board/" {
		t.Fatalf("%+v %v", published, err)
	}
	routes, _ := readRoutes(filepath.Join(c.dir, "state", "caddy.json"))
	if len(routes) != 1 || routes[0].Kind != "artifacts" || routes[0].Path != filepath.Join(c.dir, "data", "devstation", "artifacts", "site") {
		t.Fatalf("route: %+v", routes)
	}
	// Flags after positional arguments, as in the help text.
	var list []map[string]any
	if err := json.Unmarshal([]byte(run("artifacts", "cloud", "--json")), &list); err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if out := run("keep", "--json", "--", "cloud/board"); !strings.Contains(out, `"keep":true`) {
		t.Fatal(out)
	}
	// Once the route exists it decides the store, whatever XDG_DATA_HOME says.
	t.Setenv("XDG_DATA_HOME", filepath.Join(c.dir, "other"))
	if out := run("artifacts"); !strings.Contains(out, "cloud/board") {
		t.Fatalf("store not taken from the route: %q", out)
	}
	if err := Run([]string{"--config", configPath, "serve", shots, "--name", "artifacts"}, "test", &bytes.Buffer{}); err == nil {
		t.Fatal("serve replaced the artifacts route")
	}
	if out := run("unpublish", "cloud/board", "--json"); !strings.Contains(out, `"removed":["cloud/board"]`) {
		t.Fatal(out)
	}
	if err := Run([]string{"--config", configPath, "publish", shots, "--project", "Bad", "--name", "x"}, "test", &bytes.Buffer{}); err == nil {
		t.Fatal("accepted an invalid project")
	}
	run("unexpose", "artifacts")
	// A rejected publish must not add the route.
	if err := Run([]string{"--config", configPath, "publish", shots, "--project", "Bad", "--name", "x"}, "test", &bytes.Buffer{}); err == nil {
		t.Fatal("accepted an invalid project")
	}
	if routes, _ = readRoutes(filepath.Join(c.dir, "state", "caddy.json")); len(routes) != 0 {
		t.Fatalf("rejected publish changed routes: %+v", routes)
	}
}

func TestArtifactStoreMustBeASiteDirectory(t *testing.T) {
	c := testConfig(t)
	c.dir = filepath.Join(c.dir, "state")
	_ = os.MkdirAll(c.dir, 0700)
	if err := apply(c, []Route{{Name: "artifacts", Kind: "artifacts", Path: "/srv/victim/served"}}, true, func(...string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := artifactStore(c); err == nil || !strings.Contains(err.Error(), "not an artifact store") {
		t.Fatalf("accepted a foreign directory as store: %v", err)
	}
}

func TestReadCookie(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "app.cookie")
	_ = os.WriteFile(good, []byte("abc123\n"), 0600)
	if c, err := readCookie("session=@" + good); err != nil || c.Name != "session" || c.Value != "abc123" {
		t.Fatalf("%+v %v", c, err)
	}
	multi := filepath.Join(dir, "multi.cookie")
	_ = os.WriteFile(multi, []byte("a=1; b=2"), 0600)
	for _, spec := range []string{"session=abc", "=@" + good, "session=@", "session=@" + filepath.Join(dir, "missing"), "session=@" + multi} {
		if _, err := readCookie(spec); err == nil {
			t.Errorf("accepted %q", spec)
		}
	}
	var warned bytes.Buffer
	old := stderr
	stderr = &warned
	defer func() { stderr = old }()
	open := filepath.Join(dir, "open.cookie")
	_ = os.WriteFile(open, []byte("x"), 0644)
	if _, err := readCookie("s=@" + open); err != nil || !strings.Contains(warned.String(), "chmod 600") {
		t.Fatalf("no permission warning: %v %q", err, warned.String())
	}
}

func TestArtifactsRouteGetsDaemonProxy(t *testing.T) {
	c := testConfig(t)
	site := filepath.Join(c.dir, "artifacts", "site")
	older := fmt.Sprintf(`{"admin":{"listen":"unix/%s","config":{"persist":false}},"apps":{"http":{"servers":{"devstation":{"listen":["127.0.0.1:8443"],"routes":[{"@id":"devstation-artifacts","match":[{"host":["artifacts.dev.example.com"]}],"handle":[{"handler":"headers","response":{"set":{"Cache-Control":["no-cache"]}}},{"handler":"file_server","root":%q}],"terminal":true},{"handle":[{"handler":"static_response","status_code":404}],"terminal":true}],"tls_connection_policies":[{}],"automatic_https":{"disable":true}}}},"tls":{}}}`, c.socket(), site)
	if err := os.WriteFile(c.statePath(), []byte(older), 0600); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	if err := ensureArtifactsRoute(c, site, func(args ...string) error {
		if args[0] == "reload" {
			reloads++
		}
		return nil
	}); err != nil || reloads != 1 {
		t.Fatalf("adding the proxy: %v, %d reloads", err, reloads)
	}
	data, _ := os.ReadFile(c.statePath())
	if !strings.Contains(string(data), "unix/"+filepath.Join(c.dir, "daemon.sock")) || !strings.Contains(string(data), "/_devstation/*") {
		t.Fatalf("daemon proxy missing: %s", data)
	}
	// The proxy has no @id: it is not a named route, and older versions skip it.
	if routes, err := readRoutes(c.statePath()); err != nil || len(routes) != 1 || routes[0].Kind != "artifacts" || routes[0].Path != site {
		t.Fatalf("routes: %+v %v", routes, err)
	}
	// Once the proxy exists, publish leaves Caddy alone, even when other
	// settings changed since the last apply.
	c.Listen = "127.0.0.1:9443"
	if err := ensureArtifactsRoute(c, site, func(...string) error { t.Fatal("reloaded a current route"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonRoutesComments(t *testing.T) {
	h := daemonHandler(artifacts.Store{Root: t.TempDir()})
	for path, want := range map[string]int{"/_devstation/comments/p/n": http.StatusMethodNotAllowed, "/_devstation/other": http.StatusNotFound, "/p/n": http.StatusNotFound} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
	}
}
