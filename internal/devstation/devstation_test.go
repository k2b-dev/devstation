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
	old, _ := render(c, []Route{{"first", 3000}})
	if err := atomicWrite(c.statePath(), old); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	err := apply(c, []Route{{"second", 4000}}, false, func(args ...string) error {
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
	old, _ := render(c, []Route{{"first", 3000}})
	_ = atomicWrite(c.statePath(), old)
	err := apply(c, []Route{{"second", 4000}}, false, func(...string) error { return errors.New("invalid cert") })
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
	for _, args := range [][]string{{"expose", "0", "--name", "ok"}, {"expose", "3000", "--name", "../../bad"}, {"expose", "3000", "--name", "a.b"}, {"expose", "3000", "--name", "-bad"}, {"unexpose", "missing"}} {
		if err := Run(append([]string{"--config", p}, args...), "test", &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestRoutesRoundTrip(t *testing.T) {
	c := testConfig(t)
	err := apply(c, []Route{{"z", 4000}, {"a", 3000}}, true, func(...string) error { return nil })
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

func TestListShowsSavedURLsAfterConfigEdit(t *testing.T) {
	c := testConfig(t)
	configPath := filepath.Join(c.dir, "config.toml")
	changed := "domain = 'new.example.com'\nlisten = '127.0.0.1:9443'\ncertificate = '/tmp/cert'\nkey = '/tmp/key'\n"
	_ = os.WriteFile(configPath, []byte(changed), 0600)
	old := c
	old.dir = filepath.Join(c.dir, "state")
	_ = os.Mkdir(old.dir, 0700)
	data, _ := render(old, []Route{{"example", 3000}})
	_ = atomicWrite(old.statePath(), data)
	var output bytes.Buffer
	if err := Run([]string{"--config", configPath, "list", "--json"}, "test", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "https://example.dev.example.com:8443") {
		t.Fatal(output.String())
	}
}
