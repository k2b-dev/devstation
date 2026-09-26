package devstation

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/k2b-dev/devstation/internal/release"
)

const help = `devstation — local services behind HTTPS

  dev expose PORT --name NAME [--no-reload]
  dev serve PATH --name NAME [--no-reload]
  dev list [--json]
  dev unexpose NAME [--no-reload]
  dev version
  dev update [VERSION]

Global option (before command): --config PATH
Default: $XDG_CONFIG_HOME/devstation/config.toml or ~/.config/devstation/config.toml
--no-reload writes validated configuration for initial setup; it does not start Caddy.
`

func Run(args []string, version string, out io.Writer) error {
	root := flag.NewFlagSet("dev", flag.ContinueOnError)
	root.SetOutput(out)
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	// Use the Linux convention even when contributors test on macOS.
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		base = os.Getenv("XDG_CONFIG_HOME")
	} else {
		home, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		base = filepath.Join(home, ".config")
	}
	config := root.String("config", filepath.Join(base, "devstation", "config.toml"), "configuration path")
	root.Usage = func() { fmt.Fprint(out, help) }
	if err = root.Parse(args); err == flag.ErrHelp {
		return nil
	} else if err != nil {
		return err
	}
	args = root.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprint(out, help)
		return nil
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("usage: dev version")
		}
		fmt.Fprintln(out, version)
		return nil
	case "update":
		if len(args) > 2 {
			return fmt.Errorf("usage: dev update [vX.Y.Z]")
		}
		target := "latest"
		if len(args) == 2 {
			target = args[1]
		}
		return release.Update(target, out)
	case "expose", "serve", "unexpose", "list":
	default:
		return fmt.Errorf("unknown command %q; run dev --help", args[0])
	}
	c, err := LoadConfig(*config)
	if err != nil {
		return err
	}
	unlock, err := lock(c)
	if err != nil {
		return err
	}
	defer unlock()
	routes, err := readRoutes(c.statePath())
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	if args[0] == "list" {
		asJSON := flags.Bool("json", false, "machine-readable routes")
		if err = flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("usage: dev list [--json]")
		}
		type entry struct {
			Name string `json:"name"`
			Port int    `json:"port,omitempty"`
			Path string `json:"path,omitempty"`
			Kind string `json:"kind,omitempty"`
			URL  string `json:"url"`
		}
		entries := []entry{}
		urls, err := savedURLs(c.statePath())
		if err != nil {
			return err
		}
		for _, r := range routes {
			entries = append(entries, entry{r.Name, r.Port, r.Path, r.Kind, urls[r.Name]})
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(entries)
		}
		for _, e := range entries {
			target := e.Path
			if e.Kind == "" {
				target = "127.0.0.1:" + strconv.Itoa(e.Port)
			}
			fmt.Fprintf(out, "%s\t%s\t%s\n", e.Name, target, e.URL)
		}
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("missing port or name; run dev --help")
	}
	offline := flags.Bool("no-reload", false, "write validated config without reloading Caddy")
	name := ""
	if args[0] == "expose" || args[0] == "serve" {
		flags.StringVar(&name, "name", "", "DNS label (required)")
	} else {
		name = args[1]
	}
	if err = flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if !labelPattern.MatchString(name) {
		return fmt.Errorf("name must be a lowercase DNS label (1–63 letters, digits or hyphens; no leading/trailing hyphen)")
	}
	port := 0
	path, kind := "", ""
	if args[0] == "expose" {
		port, err = strconv.Atoi(args[1])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
	} else if args[0] == "serve" {
		path, err = filepath.Abs(args[1])
		if err != nil {
			return err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("static path: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("static path: %w", err)
		}
		if info.IsDir() {
			kind = "directory"
		} else if info.Mode().IsRegular() {
			kind = "file"
		} else {
			return fmt.Errorf("static path must be a regular file or directory")
		}
	}
	found := false
	next := []Route{}
	for _, r := range routes {
		if r.Name == name {
			found = true
		} else {
			next = append(next, r)
		}
	}
	if args[0] == "unexpose" && !found {
		return fmt.Errorf("route %q does not exist", name)
	}
	if args[0] == "expose" || args[0] == "serve" {
		next = append(next, Route{Name: name, Port: port, Path: path, Kind: kind})
	}
	if err = apply(c, next, *offline, runCaddy); err != nil {
		return err
	}
	if *offline {
		fmt.Fprintln(out, "Saved configuration; Caddy has not been reloaded:", c.statePath())
	} else if args[0] == "expose" || args[0] == "serve" {
		fmt.Fprintln(out, c.url(name))
	} else {
		fmt.Fprintln(out, "Removed", name)
	}
	return nil
}
