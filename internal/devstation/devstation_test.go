package devstation

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
