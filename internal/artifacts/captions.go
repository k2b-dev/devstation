package artifacts

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// captionsFile gives the images of its directory a short title and at most a
// sentence of context, shown next to the gallery row and in the viewer:
//
//	01-list-empty: Empty list
//	No entries yet; "New" sits top right.
//
//	02-dialog: New entry
//
// Blocks are separated by blank lines. The key is a gallery row (the file name
// without theme and width) or another image's name without its extension.
const captionsFile = "captions.txt"

const (
	maxCaptionTitle = 60
	maxCaptionText  = 160
	maxCaptionsSize = 64 << 10
)

type caption struct{ Title, Text string }

// parseCaptions reads a captions file. Captions stay short on purpose: longer
// titles and texts are cut, with a warning.
func parseCaptions(name string, src []byte, known map[string]bool) (map[string]caption, []string) {
	out := map[string]caption{}
	var warnings []string
	warn := func(format string, args ...any) { warnings = append(warnings, name+": "+fmt.Sprintf(format, args...)) }
	if len(src) > maxCaptionsSize {
		warn("larger than %d KiB, ignored", maxCaptionsSize>>10)
		return out, warnings
	}
	// Blocks end at lines that are blank, even if they hold spaces.
	var blocks [][]string
	var block []string
	for _, l := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			if block != nil {
				blocks, block = append(blocks, block), nil
			}
			continue
		}
		block = append(block, l)
	}
	if block != nil {
		blocks = append(blocks, block)
	}
	for _, lines := range blocks {
		lines[0] = strings.TrimSpace(lines[0])
		key, title, ok := strings.Cut(lines[0], ":")
		key, title = strings.TrimSpace(key), strings.TrimSpace(title)
		if !ok || key == "" || title == "" {
			warn("%q is not \"NAME: Title\"", lines[0])
			continue
		}
		if !known[key] {
			warn("no image for %q", key)
			continue
		}
		var text []string
		for _, l := range lines[1:] {
			text = append(text, strings.TrimSpace(l))
		}
		c := caption{Title: title, Text: strings.Join(text, " ")}
		if utf8.RuneCountInString(c.Title) > maxCaptionTitle {
			c.Title = cut(c.Title, maxCaptionTitle)
			warn("title of %q is longer than %d characters, shortened", key, maxCaptionTitle)
		}
		if utf8.RuneCountInString(c.Text) > maxCaptionText {
			c.Text = cut(c.Text, maxCaptionText)
			warn("text of %q is longer than %d characters, shortened", key, maxCaptionText)
		}
		out[key] = c
	}
	return out, warnings
}

func cut(s string, n int) string {
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// captionKey is the name a caption uses for an image: its gallery row, or its
// name without the extension.
func captionKey(p string) string {
	if sh, ok := parseShot(p); ok {
		return sh.row
	}
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}
