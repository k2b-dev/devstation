package devstation

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/k2b-dev/devstation/internal/shot"
)

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

const shotUsage = "usage: dev shot [LABEL=]URL... --out DIR [--themes light,dark] [--widths 1440,390] [--cookie NAME=@FILE]... [--theme-cookie NAME] [--eval JS] [--click SELECTOR]... [--hover SELECTOR] [--wait-for SELECTOR] [--full-page] [--height PX] [--scale N] [--browser PATH] [--timeout DURATION] [--json]"

func runShot(configPath string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("shot", flag.ContinueOnError)
	flags.SetOutput(out)
	var o shot.Options
	var cookies, clicks stringList
	themes := flags.String("themes", "light,dark", "color schemes: light, dark, hell, dunkel")
	widths := flags.String("widths", "1440,390", "viewport widths in CSS pixels")
	flags.StringVar(&o.Out, "out", "", "directory for the PNG files (required)")
	flags.IntVar(&o.Height, "height", 0, "viewport height (default 900, or 844 below 600 px width)")
	flags.BoolVar(&o.FullPage, "full-page", false, "capture the whole page, not just the viewport")
	flags.Float64Var(&o.Scale, "scale", 1, "device pixel ratio; file names keep the CSS width")
	flags.Var(&cookies, "cookie", "NAME=@FILE: cookie whose value is read from FILE (repeatable)")
	flags.StringVar(&o.ThemeCookie, "theme-cookie", "", "cookie set to light or dark for each theme")
	flags.StringVar(&o.Eval, "eval", "", "JavaScript to run after the page loaded")
	flags.Var(&clicks, "click", "CSS selector to click, in order (repeatable)")
	flags.StringVar(&o.Hover, "hover", "", "CSS selector to hover last")
	flags.StringVar(&o.WaitFor, "wait-for", "", "CSS selector to wait for before capturing")
	flags.StringVar(&o.Browser, "browser", "", "Chrome or Chromium binary (default: $DEVSTATION_BROWSER, PATH, Playwright's download)")
	timeout := flags.Duration("timeout", 90*time.Second, "limit for the whole command")
	asJSON := flags.Bool("json", false, "machine-readable output")
	positional, err := parseMixed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 || o.Out == "" {
		return fmt.Errorf(shotUsage)
	}
	o.Clicks = clicks
	for _, arg := range positional {
		t, err := shot.ParseTarget(arg)
		if err != nil {
			return err
		}
		o.Targets = append(o.Targets, t)
	}
	for _, t := range strings.Split(*themes, ",") {
		o.Themes = append(o.Themes, strings.ToLower(strings.TrimSpace(t)))
	}
	for _, w := range strings.Split(*widths, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(w))
		if err != nil {
			return fmt.Errorf("invalid width %q", w)
		}
		o.Widths = append(o.Widths, n)
	}
	for _, spec := range cookies {
		c, err := readCookie(spec)
		if err != nil {
			return err
		}
		o.Cookies = append(o.Cookies, c)
	}
	// Reach this host's own preview names through Caddy even when the host
	// cannot resolve them itself.
	if c, err := LoadConfig(configPath); err == nil {
		if host, _, err := net.SplitHostPort(c.Listen); err == nil {
			ip := net.ParseIP(host)
			switch {
			case ip.IsUnspecified() && ip.To4() != nil:
				host = "127.0.0.1"
			case ip.IsUnspecified():
				host = "[::1]"
			case strings.Contains(host, ":"):
				host = "[" + host + "]"
			}
			o.Resolver = "MAP *." + c.Domain + " " + host
		}
	}
	// Ctrl-C, SIGTERM and --timeout all stop the browser and remove its profile.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	r, err := shot.Run(ctx, o)
	for _, w := range r.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	if *asJSON {
		// Shots taken before an error are listed too.
		result := struct {
			shot.Result
			Error string `json:"error,omitempty"`
		}{Result: r}
		if err != nil {
			result.Error = err.Error()
		}
		if encodeErr := json.NewEncoder(out).Encode(result); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	for _, s := range r.Files {
		fmt.Fprintln(out, s.File)
	}
	return err
}

// readCookie reads NAME=@FILE; the file holds only the cookie value.
func readCookie(spec string) (shot.Cookie, error) {
	name, file, ok := strings.Cut(spec, "=@")
	if !ok || name == "" || file == "" {
		return shot.Cookie{}, fmt.Errorf("--cookie takes NAME=@FILE, so the value never appears in the command line")
	}
	if strings.HasPrefix(file, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return shot.Cookie{}, err
		}
		file = home + file[1:]
	}
	info, err := os.Stat(file)
	if err != nil {
		return shot.Cookie{}, fmt.Errorf("--cookie %s: %w", name, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(stderr, "warning: cookie file %s is readable by other users; use chmod 600\n", file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return shot.Cookie{}, err
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsAny(value, ";\r\n") {
		return shot.Cookie{}, fmt.Errorf("--cookie %s: the file must hold a single cookie value", name)
	}
	return shot.Cookie{Name: name, Value: value}, nil
}
