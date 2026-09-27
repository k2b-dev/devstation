package devstation

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/k2b-dev/devstation/internal/artifacts"
)

const commentsUsage = `usage: dev comments PROJECT/NAME [--all] [--images DIR] [--json]
       dev comments resolve PROJECT/NAME ID... [--reopen]`

type commentJSON struct {
	artifacts.Comment
	URL   string `json:"url"`
	Image string `json:"image,omitempty"`
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
		// The version page opens the image in its viewer from the anchor.
		link := fmt.Sprintf("%s/%s/%s/v/%d/#%s", base, p, n, cm.Version, url.PathEscape(cm.Path))
		list = append(list, commentJSON{Comment: cm, URL: link, Image: annotated[fmt.Sprintf("v%d/%s", cm.Version, cm.Path)]})
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
		state := ""
		if cm.Resolved {
			state = " (resolved)"
		}
		where := ""
		if cm.X != nil {
			where = fmt.Sprintf(" at %.0f%%, %.0f%%", *cm.X*100, *cm.Y*100)
		}
		fmt.Fprintf(out, "\n#%d  v%d  %s%s%s  [%s]\n", cm.Number, cm.Version, cm.Path, where, state, cm.ID)
		for _, line := range strings.Split(cm.Text, "\n") {
			fmt.Fprintf(out, "    %s\n", line)
		}
		fmt.Fprintf(out, "    %s\n", cm.URL)
		if cm.Image != "" {
			fmt.Fprintf(out, "    pinned image: %s\n", cm.Image)
		}
	}
	return nil
}
