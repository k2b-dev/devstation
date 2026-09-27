package devstation

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/k2b-dev/devstation/internal/artifacts"
)

// artifactsRoute is the fixed route name that serves the artifact store.
const artifactsRoute = "artifacts"

// stderr receives warnings so that stdout carries only the URL or JSON.
var stderr io.Writer = os.Stderr

type artifactJSON struct {
	Project    string     `json:"project"`
	Name       string     `json:"name"`
	Title      string     `json:"title,omitempty"`
	Version    int        `json:"version"`
	URL        string     `json:"url"`
	VersionURL string     `json:"version_url"`
	CompareURL string     `json:"compare_url,omitempty"`
	ProjectURL string     `json:"project_url"`
	Summary    string     `json:"summary"`
	Files      int        `json:"files"`
	Bytes      int64      `json:"bytes"`
	Link       string     `json:"link,omitempty"`
	Keep       bool       `json:"keep"`
	Updated    time.Time  `json:"updated"`
	Expires    *time.Time `json:"expires"`
	Comments   int        `json:"open_comments"`
}

func describe(base string, m artifacts.Meta) artifactJSON {
	v := m.Versions[len(m.Versions)-1]
	out := artifactJSON{
		Project: m.Project, Name: m.Name, Title: m.Title, Version: v.N,
		URL: artifacts.ArtifactURL(base, m), VersionURL: artifacts.VersionURL(base, m, v.N),
		CompareURL: artifacts.CompareURL(base, m), ProjectURL: base + "/" + m.Project + "/",
		Summary: artifacts.Summary(m), Files: len(v.Files), Bytes: v.Bytes, Link: m.Link, Keep: m.Keep, Updated: m.Updated,
	}
	if !m.Keep {
		e := m.Expires()
		out.Expires = &e
	}
	return out
}

// artifactStore returns the store the "artifacts" route serves, so the route is the
// single source of truth once it exists. Before the first publish, the store
// location follows XDG_DATA_HOME. It fails if the route name is used for something else.
func artifactStore(c Config) (artifacts.Store, string, bool, error) {
	routes, err := readRoutes(c.statePath())
	if err != nil {
		return artifacts.Store{}, "", false, err
	}
	for _, r := range routes {
		if r.Name != artifactsRoute {
			continue
		}
		if r.Kind != "artifacts" {
			return artifacts.Store{}, "", false, fmt.Errorf("route %q is in use for something else; remove it with `dev unexpose %s` so artifacts can use it", artifactsRoute, artifactsRoute)
		}
		if filepath.Base(r.Path) != "site" {
			return artifacts.Store{}, "", false, fmt.Errorf("route %q serves %s, which is not an artifact store's site directory; remove it with `dev unexpose %s`", artifactsRoute, r.Path, artifactsRoute)
		}
		urls, err := savedURLs(c.statePath())
		if err != nil {
			return artifacts.Store{}, "", false, err
		}
		return artifacts.Store{Root: filepath.Dir(r.Path)}, urls[artifactsRoute], true, nil
	}
	root, err := artifacts.DefaultRoot()
	return artifacts.Store{Root: root}, c.url(artifactsRoute), false, err
}

// parseMixed parses flags that may appear before, between or after the
// positional arguments, as in `dev artifacts cloud --json`. Everything after
// "--" is positional.
func parseMixed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional, rest []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, rest = args[:i], args[i+1:]
	}
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		if flags.NArg() == 0 {
			return append(positional, rest...), nil
		}
		positional = append(positional, flags.Arg(0))
		args = flags.Args()[1:]
	}
}

func runArtifacts(c Config, args []string, out io.Writer) error {
	store, base, routed, err := artifactStore(c)
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "machine-readable output")
	var o artifacts.Options
	expired := false
	switch args[0] {
	case "publish":
		flags.StringVar(&o.Project, "project", "", "project, a DNS label (required)")
		flags.StringVar(&o.Name, "name", "", "artifact name, a DNS label (required)")
		flags.StringVar(&o.Title, "title", "", "title shown on the pages; kept for later versions")
		flags.StringVar(&o.Link, "link", "", "related http(s) URL, e.g. a pull request")
		flags.BoolVar(&o.Keep, "keep", false, "keep until unpublished instead of 14 days")
		flags.BoolVar(&o.AllowSensitive, "allow-sensitive", false, "publish files whose names look like secrets")
	case "unpublish":
		flags.BoolVar(&expired, "expired", false, "remove all expired artifacts")
	}
	positional, err := parseMixed(flags, args[1:])
	if err != nil {
		return err
	}
	switch args[0] {
	case "publish":
		o.Paths = positional
		if o.Project == "" || o.Name == "" || len(o.Paths) == 0 {
			return fmt.Errorf("usage: dev publish PATH... --project PROJECT --name NAME [--title TEXT] [--link URL] [--keep] [--allow-sensitive] [--json]")
		}
		r, err := store.Publish(o)
		if err != nil {
			return err
		}
		// The files are in place before the route appears or is updated, so a
		// rejected publish never reloads Caddy.
		if err = ensureArtifactsRoute(c, store.Site(), runCaddy); err != nil {
			if !routed {
				return fmt.Errorf("published %s/%s, but it is not served yet: the %q route could not be added (%w); fix the cause and publish again", o.Project, o.Name, artifactsRoute, err)
			}
			fmt.Fprintf(stderr, "warning: published, but updating the %q route failed: %v\n", artifactsRoute, err)
		}
		for _, s := range r.Skipped {
			fmt.Fprintln(stderr, "skipped (hidden or not a regular file):", s)
		}
		for _, w := range r.Warnings {
			fmt.Fprintln(stderr, "warning:", w)
		}
		for _, ref := range r.Removed {
			fmt.Fprintln(stderr, "removed expired artifact:", ref)
		}
		d := describe(base, r.Meta)
		if *asJSON {
			return json.NewEncoder(out).Encode(struct {
				artifactJSON
				Skipped  []string `json:"skipped"`
				Warnings []string `json:"warnings"`
			}{d, nonNil(r.Skipped), nonNil(r.Warnings)})
		}
		fmt.Fprintln(out, d.URL)
		return nil
	case "artifacts":
		if len(positional) > 1 {
			return fmt.Errorf("usage: dev artifacts [PROJECT] [--json]")
		}
		project := ""
		if len(positional) == 1 {
			project = positional[0]
			if !labelPattern.MatchString(project) {
				return fmt.Errorf("project must be a lowercase DNS label, got %q", project)
			}
		}
		metas, damaged, err := store.List(project)
		if err != nil {
			return err
		}
		for _, ref := range damaged {
			fmt.Fprintf(stderr, "warning: %s has damaged metadata and is not listed; remove it with `dev unpublish %s`\n", ref, ref)
		}
		list := []artifactJSON{}
		for _, m := range metas {
			d := describe(base, m)
			if cs, err := store.Comments(m.Project, m.Name); err == nil {
				d.Comments = artifacts.OpenComments(cs)
			}
			list = append(list, d)
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(list)
		}
		for _, a := range list {
			life := "kept"
			if a.Expires != nil {
				life = "expires " + a.Expires.Format("2006-01-02")
			}
			if a.Comments > 0 {
				life += fmt.Sprintf(", %d open comments", a.Comments)
			}
			fmt.Fprintf(out, "%s/%s\tv%d\t%s\t%s\t%s\n", a.Project, a.Name, a.Version, a.Summary, life, a.URL)
		}
		return nil
	case "keep":
		if len(positional) != 1 {
			return fmt.Errorf("usage: dev keep PROJECT/NAME [--json]")
		}
		p, n, err := artifacts.ParseRef(positional[0])
		if err != nil {
			return err
		}
		m, err := store.Keep(p, n)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(describe(base, m))
		}
		fmt.Fprintln(out, "Keeping", p+"/"+n)
		return nil
	case "unpublish":
		if expired == (len(positional) > 0) {
			return fmt.Errorf("usage: dev unpublish PROJECT/NAME... | --expired [--json]")
		}
		removed, err := store.Unpublish(positional)
		if err != nil {
			for _, ref := range removed {
				fmt.Fprintln(stderr, "removed", ref)
			}
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(map[string][]string{"removed": nonNil(removed)})
		}
		for _, ref := range removed {
			fmt.Fprintln(out, "Removed", ref)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// ensureArtifactsRoute adds the "artifacts" route on first use, and the proxy to
// `dev daemon` where a configuration was saved before it existed. It is the
// only artifact command that changes Caddy's configuration.
func ensureArtifactsRoute(c Config, site string, run runner) error {
	check := func(routes []Route) (bool, error) {
		for _, r := range routes {
			if r.Name != artifactsRoute {
				continue
			}
			if r.Kind == "artifacts" && r.Path == site {
				return true, nil
			}
			if r.Kind == "artifacts" {
				return false, fmt.Errorf("route %q already serves artifacts from %s; this command used %s (check HOME and XDG_DATA_HOME)", artifactsRoute, r.Path, site)
			}
			return false, fmt.Errorf("route %q is in use for something else; remove it with `dev unexpose %s` so artifacts can use it", artifactsRoute, artifactsRoute)
		}
		return false, nil
	}
	current := func(routes []Route) (bool, error) {
		ok, err := check(routes)
		if !ok || err != nil {
			return false, err
		}
		return daemonRouted(c.statePath()), nil
	}
	routes, err := readRoutes(c.statePath())
	if err != nil {
		return err
	}
	if ok, err := current(routes); ok || err != nil {
		return err
	}
	if err = os.MkdirAll(site, 0700); err != nil {
		return err
	}
	unlock, err := lockWait(c, 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if routes, err = readRoutes(c.statePath()); err != nil {
		return err
	}
	if ok, err := current(routes); ok || err != nil {
		return err
	}
	if exists, _ := check(routes); exists {
		return apply(c, routes, false, run)
	}
	return apply(c, append(routes, Route{Name: artifactsRoute, Kind: "artifacts", Path: site}), false, run)
}

// lockWait retries the non-blocking route lock, so parallel first publishes
// do not fail on each other.
func lockWait(c Config, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		unlock, err := lock(c)
		if err == nil || !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return unlock, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
