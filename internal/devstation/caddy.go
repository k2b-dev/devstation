package devstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Route struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	Path string `json:"path,omitempty"`
	Kind string `json:"kind,omitempty"`
}
type caddyRoute struct {
	ID       string                `json:"@id,omitempty"`
	Match    []map[string][]string `json:"match,omitempty"`
	Handle   []handler             `json:"handle"`
	Terminal bool                  `json:"terminal"`
}
type handler struct {
	Handler    string     `json:"handler"`
	Upstreams  []upstream `json:"upstreams,omitempty"`
	StatusCode int        `json:"status_code,omitempty"`
	Root       string     `json:"root,omitempty"`
	URI        string     `json:"uri,omitempty"`
	Response   *headerOps `json:"response,omitempty"`
}
type headerOps struct {
	Set map[string][]string `json:"set"`
}

// Artifact pages change in place under stable URLs, so browsers revalidate
// them with the ETag that file_server sends. nosniff keeps files without a
// known type, such as extensionless notes, from running as HTML.
func artifactHeaders() *headerOps {
	return &headerOps{Set: map[string][]string{"Cache-Control": {"no-cache"}, "X-Content-Type-Options": {"nosniff"}}}
}

type upstream struct {
	Dial string `json:"dial"`
}
type server struct {
	Listen    []string         `json:"listen"`
	Routes    []caddyRoute     `json:"routes"`
	TLS       []map[string]any `json:"tls_connection_policies"`
	AutoHTTPS map[string]bool  `json:"automatic_https"`
}
type document struct {
	Admin struct {
		Listen string          `json:"listen"`
		Config map[string]bool `json:"config"`
	} `json:"admin"`
	Apps struct {
		HTTP struct {
			Servers map[string]server `json:"servers"`
		} `json:"http"`
		TLS map[string]any `json:"tls"`
	} `json:"apps"`
}

func render(c Config, routes []Route) ([]byte, error) {
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	var d document
	d.Admin.Listen = "unix/" + c.socket()
	d.Admin.Config = map[string]bool{"persist": false}
	s := server{Listen: []string{c.Listen}, TLS: []map[string]any{{}}, AutoHTTPS: map[string]bool{"disable": true}}
	for _, r := range routes {
		var handles []handler
		switch r.Kind {
		case "", "proxy":
			handles = []handler{{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port))}}}}
		case "directory":
			handles = []handler{{Handler: "file_server", Root: r.Path}}
		case "file":
			handles = []handler{{Handler: "rewrite", URI: "/" + url.PathEscape(filepath.Base(r.Path))}, {Handler: "file_server", Root: filepath.Dir(r.Path)}}
		case "artifacts":
			handles = []handler{{Handler: "headers", Response: artifactHeaders()}, {Handler: "file_server", Root: r.Path}}
			// /_devstation/ goes to `dev daemon`. The route has no @id, so
			// versions before the daemon skip it and still work after a rollback.
			s.Routes = append(s.Routes, caddyRoute{Match: []map[string][]string{{"host": {r.Name + "." + c.Domain}, "path": {daemonPath}}}, Handle: []handler{{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: "unix/" + daemonSocket(c.dir)}}}}, Terminal: true})
		default:
			return nil, fmt.Errorf("invalid route kind %q", r.Kind)
		}
		s.Routes = append(s.Routes, caddyRoute{ID: "devstation-" + r.Name, Match: []map[string][]string{{"host": {r.Name + "." + c.Domain}}}, Handle: handles, Terminal: true})
	}
	s.Routes = append(s.Routes, caddyRoute{Handle: []handler{{Handler: "static_response", StatusCode: 404}}, Terminal: true})
	d.Apps.HTTP.Servers = map[string]server{"devstation": s}
	d.Apps.TLS = map[string]any{"certificates": map[string]any{"load_files": []map[string]string{{"certificate": c.Certificate, "key": c.Key}}}}
	return json.MarshalIndent(d, "", "  ")
}

const daemonPath = "/_devstation/*"

// daemonSocket is where `dev daemon` listens, next to the Caddy admin socket.
func daemonSocket(stateDir string) string { return filepath.Join(stateDir, "daemon.sock") }

// daemonRouted reports whether the saved configuration sends /_devstation/ to
// `dev daemon`; configurations saved before the daemon existed do not.
func daemonRouted(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var d document
	if json.Unmarshal(data, &d) != nil {
		return false
	}
	for _, r := range d.Apps.HTTP.Servers["devstation"].Routes {
		if r.ID == "" && len(r.Match) == 1 && len(r.Match[0]["path"]) == 1 && r.Match[0]["path"][0] == daemonPath {
			return true
		}
	}
	return false
}

func readRoutes(path string) ([]Route, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Route{}, nil
	}
	if err != nil {
		return nil, err
	}
	var d document
	if err = json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("invalid saved Caddy configuration: %w", err)
	}
	s, ok := d.Apps.HTTP.Servers["devstation"]
	if !ok {
		return nil, fmt.Errorf("not a devstation configuration")
	}
	routes := []Route{}
	seen := map[string]bool{}
	for _, r := range s.Routes {
		if r.ID == "" {
			continue
		}
		const prefix = "devstation-"
		if len(r.ID) <= len(prefix) || r.ID[:len(prefix)] != prefix {
			return nil, fmt.Errorf("unexpected saved route %q", r.ID)
		}
		name := r.ID[len(prefix):]
		if !labelPattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("invalid saved route %q", name)
		}
		entry := Route{Name: name}
		switch {
		case len(r.Handle) == 1 && r.Handle[0].Handler == "reverse_proxy" && len(r.Handle[0].Upstreams) == 1:
			host, p, err := net.SplitHostPort(r.Handle[0].Upstreams[0].Dial)
			port, perr := strconv.Atoi(p)
			if err != nil || perr != nil || host != "127.0.0.1" || port < 1 || port > 65535 {
				return nil, fmt.Errorf("invalid saved upstream")
			}
			entry.Port = port
		case len(r.Handle) == 2 && r.Handle[0].Handler == "headers" && r.Handle[1].Handler == "file_server":
			// Recognized by shape: the next apply writes the current header
			// set, so routes saved by older versions keep working.
			h := r.Handle[0]
			if h.Response == nil || len(h.Response.Set) == 0 || h.Root != "" || h.URI != "" || h.Upstreams != nil || h.StatusCode != 0 {
				return nil, fmt.Errorf("invalid saved route %q", name)
			}
			entry.Kind = "artifacts"
			entry.Path = r.Handle[1].Root
		case len(r.Handle) == 1 && r.Handle[0].Handler == "file_server":
			entry.Kind = "directory"
			entry.Path = r.Handle[0].Root
		case len(r.Handle) == 2 && r.Handle[0].Handler == "rewrite" && r.Handle[1].Handler == "file_server":
			entry.Kind = "file"
			root := r.Handle[1].Root
			uri, err := url.PathUnescape(r.Handle[0].URI)
			if err != nil {
				return nil, fmt.Errorf("invalid saved file URI: %w", err)
			}
			if uri == "/" || !strings.HasPrefix(uri, "/") || filepath.Base(uri) != strings.TrimPrefix(uri, "/") {
				return nil, fmt.Errorf("invalid saved file route %q", name)
			}
			entry.Path = filepath.Join(root, strings.TrimPrefix(uri, "/"))
		default:
			return nil, fmt.Errorf("invalid saved route %q", name)
		}
		if entry.Kind != "" && (!filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path) {
			return nil, fmt.Errorf("invalid saved static path")
		}
		seen[name] = true
		routes = append(routes, entry)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes, nil
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".devstation-*")
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

func lock(c Config) (func(), error) {
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(c.dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another dev command is running: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

type runner func(...string) error

func runCaddy(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "caddy", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy %s failed: %w\n%s", args[0], err, output)
	}
	return nil
}

func apply(c Config, routes []Route, offline bool, run runner) error {
	data, err := render(c, routes)
	if err != nil {
		return err
	}
	candidate, err := os.CreateTemp(c.dir, ".candidate-*.json")
	if err != nil {
		return err
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	if _, err = candidate.Write(data); err != nil {
		candidate.Close()
		return err
	}
	if err = candidate.Close(); err != nil {
		return err
	}
	if err = run("validate", "--config", candidatePath); err != nil {
		return err
	}
	old, err := os.ReadFile(c.statePath())
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = atomicWrite(c.statePath(), data); err != nil {
		return err
	}
	if offline {
		return nil
	}
	if err = run("reload", "--config", c.statePath(), "--address", "unix/"+c.socket(), "--force"); err == nil {
		return nil
	}
	reloadErr := err
	if !existed {
		if err = os.Remove(c.statePath()); err != nil {
			return errors.Join(reloadErr, err)
		}
		return fmt.Errorf("reload failed; initial configuration removed (use --no-reload for first setup): %w", reloadErr)
	}
	if err = atomicWrite(c.statePath(), old); err != nil {
		return errors.Join(reloadErr, fmt.Errorf("RESTORE FAILED: %w", err))
	}
	// A timed-out reload may already have reached Caddy. Reconcile the old state.
	err = run("reload", "--config", c.statePath(), "--address", "unix/"+c.socket(), "--force")
	if err != nil {
		return errors.Join(reloadErr, fmt.Errorf("saved configuration restored, but live state is uncertain: %w", err))
	}
	return fmt.Errorf("reload failed; previous configuration restored: %w", reloadErr)
}

// Report saved URLs even if TOML has been edited but not applied yet.
func savedURLs(path string) (map[string]string, error) {
	urls := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return urls, nil
	}
	if err != nil {
		return nil, err
	}
	var d document
	if err = json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	s, ok := d.Apps.HTTP.Servers["devstation"]
	if !ok || len(s.Listen) != 1 {
		return nil, fmt.Errorf("invalid saved listener")
	}
	_, port, err := net.SplitHostPort(s.Listen[0])
	if err != nil {
		return nil, err
	}
	for _, r := range s.Routes {
		if r.ID == "" {
			continue
		}
		if len(r.Match) != 1 || len(r.Match[0]["host"]) != 1 {
			return nil, fmt.Errorf("invalid saved host matcher")
		}
		suffix := ""
		if port != "443" {
			suffix = ":" + port
		}
		urls[strings.TrimPrefix(r.ID, "devstation-")] = "https://" + r.Match[0]["host"][0] + suffix
	}
	return urls, nil
}
