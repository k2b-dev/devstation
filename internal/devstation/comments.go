package devstation

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/k2b-dev/devstation/internal/artifacts"
)

const commentsUsage = `usage: dev comments PROJECT/NAME [--all] [--images DIR] [--json]
       dev comments resolve PROJECT/NAME ID... [--reopen]`

type commentJSON struct {
	artifacts.Comment
	URL    string                 `json:"url"`
	Image  string                 `json:"image,omitempty"`
	Source []artifacts.SourceLine `json:"source,omitempty"` // plan comments: the lines they point at
	More   int                    `json:"source_more,omitempty"`
	Shot   string                 `json:"shot,omitempty"` // mockup comments: a dev shot command for that state
}

func runComments(c Config, args []string, out io.Writer) error {
	store, base, _, err := artifactStore(c)
	if err != nil {
		return err
	}
	sub := ""
	if len(args) > 0 && args[0] == "resolve" {
		sub, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("comments", flag.ContinueOnError)
	flags.SetOutput(out)
	if sub == "resolve" {
		reopen := flags.Bool("reopen", false, "mark the comments as open again")
		positional, err := parseMixed(flags, args)
		if err != nil {
			return err
		}
		if len(positional) < 2 {
			return errors.New(commentsUsage)
		}
		p, n, err := artifacts.ParseRef(positional[0])
		if err != nil {
			return err
		}
		if err = store.Resolve(p, n, positional[1:], !*reopen); err != nil {
			return err
		}
		fmt.Fprintf(out, "Updated %d comment(s)\n", len(positional)-1)
		return nil
	}
	all := flags.Bool("all", false, "include resolved comments")
	images := flags.String("images", "", "write copies of commented images with numbered pins into DIR")
	asJSON := flags.Bool("json", false, "machine-readable output")
	positional, err := parseMixed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(commentsUsage)
	}
	p, n, err := artifacts.ParseRef(positional[0])
	if err != nil {
		return err
	}
	comments, err := store.Comments(p, n)
	if err != nil {
		return err
	}
	shown := []artifacts.Comment{}
	for _, cm := range comments {
		if *all || !cm.Resolved {
			shown = append(shown, cm)
		}
	}
	annotated := map[string]string{}
	if *images != "" {
		if annotated, err = store.Annotate(p, n, shown, *images); err != nil {
			return err
		}
	}
	list := []commentJSON{}
	for _, cm := range shown {
		artifact := fmt.Sprintf("%s/%s/%s/", base, p, n)
		// The version page opens the image in its viewer from the anchor.
		j := commentJSON{Comment: cm, URL: fmt.Sprintf("%sv/%d/#%s", artifact, cm.Version, url.PathEscape(cm.Path)), Image: annotated[fmt.Sprintf("v%d/%s", cm.Version, cm.Path)]}
		switch a := cm.Anchor; {
		case a != nil && a.Line > 0:
			j.URL = fmt.Sprintf("%sv/%d/#comment-%s", artifact, cm.Version, cm.ID)
			if j.Source, j.More, err = store.Source(p, n, cm.Version, cm.Path, a.Line, a.EndLine); err != nil {
				return err
			}
		case strings.EqualFold(path.Ext(cm.Path), ".md") || strings.EqualFold(path.Ext(cm.Path), ".markdown"): // opens the plan's comment panel
			j.URL = fmt.Sprintf("%sv/%d/#comment-%s", artifact, cm.Version, cm.ID)
		case a != nil:
			j.URL = fmt.Sprintf("%sreview/%d/#comment-%s", artifact, cm.Version, cm.ID)
			j.Shot = shotCommand(fmt.Sprintf("%sv/%d/%s%s", artifact, cm.Version, escapeSlashPath(cm.Path), a.Route), a)
		}
		list = append(list, j)
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(struct {
			Project  string        `json:"project"`
			Name     string        `json:"name"`
			Open     int           `json:"open_comments"`
			Comments []commentJSON `json:"comments"`
		}{p, n, artifacts.OpenComments(comments), list})
	}
	fmt.Fprintf(out, "%s/%s: %d open comment(s)\n", p, n, artifacts.OpenComments(comments))
	for _, cm := range list {
		printComment(out, cm)
	}
	return nil
}

// printComment writes one comment: a header naming what it points at, labeled
// details, the text as "> " lines (so it cannot pass for details), and links.
func printComment(out io.Writer, cm commentJSON) {
	a := cm.Anchor
	where := cm.Path
	switch {
	case a != nil && a.Line > 0:
		where += fmt.Sprintf(":%d", a.Line)
		if a.EndLine > a.Line {
			where += fmt.Sprintf("-%d", a.EndLine)
		}
	case cm.X != nil && a == nil:
		where += fmt.Sprintf(" at %.0f%%, %.0f%%", *cm.X*100, *cm.Y*100)
	}
	state := ""
	if cm.Resolved {
		state = " (resolved)"
	}
	fmt.Fprintf(out, "\n#%d  v%d  %s  [%s]%s\n", cm.Number, cm.Version, where, cm.ID, state)
	field := func(label, value string) {
		if value != "" {
			fmt.Fprintf(out, "    %-9s %s\n", label+":", value)
		}
	}
	if a != nil {
		if a.Route != "" { // untrusted, so quoted and out of the header
			field("route", fmt.Sprintf("%q", a.Route))
		}
		if a.Width > 0 {
			field("viewport", fmt.Sprintf("%d×%d %s", a.Width, a.Height, a.Theme))
		}
		field("steps", strings.Join(a.Steps, " → "))
		field("context", a.Context)
		if a.Quote != "" {
			label := "quote"
			if a.Selector != "" {
				label = "element"
			}
			field(label, fmt.Sprintf("%q", a.Quote))
		}
		field("selector", a.Selector)
		if a.Selector != "" && cm.X != nil {
			field("at", fmt.Sprintf("%.0f%%, %.0f%% of the element", *cm.X*100, *cm.Y*100))
		}
	}
	for i, l := range cm.Source {
		label := ""
		if i == 0 {
			label = "source:"
		}
		fmt.Fprintf(out, "    %-9s %d | %s\n", label, l.Line, l.Text)
	}
	if cm.More > 0 {
		fmt.Fprintf(out, "    %-9s … %d more lines\n", "", cm.More)
	}
	for _, line := range strings.Split(cm.Text, "\n") {
		fmt.Fprintf(out, "    > %s\n", line)
	}
	field("link", cm.URL)
	field("image", cm.Image)
	field("shot", cm.Shot)
}

// shotCommand is a dev shot command that opens a mockup page in the state a
// comment was made in: same viewport and theme, the recorded clicks, and the
// element under the pointer.
func shotCommand(pageURL string, a *artifacts.Anchor) string {
	args := []string{"dev", "shot", shellQuote(pageURL), "--themes", a.Theme, "--widths", fmt.Sprint(a.Width), "--height", fmt.Sprint(a.Height)}
	for _, s := range a.Steps {
		args = append(args, "--click", shellQuote(s))
	}
	if a.Selector != "" {
		args = append(args, "--hover", shellQuote(a.Selector))
	}
	return strings.Join(append(args, "--out", "DIR"), " ")
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// escapeSlashPath escapes each segment of a published path for a URL.
func escapeSlashPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
