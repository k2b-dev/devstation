package artifacts

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Anchor says what inside a file a comment means, beyond an image pin. On a
// Markdown plan it is the source lines and the selected text. On an HTML
// mockup it is the page, the element picked there, the clicks that led to
// that state, and the viewport; the comment's X and Y are then fractions of
// the element's box.
type Anchor struct {
	Line     int      `json:"line,omitempty"`     // plan: first source line, 1-based
	EndLine  int      `json:"end_line,omitempty"` // plan: last source line
	Quote    string   `json:"quote,omitempty"`    // plan: selected text; mockup: the element's text
	Context  string   `json:"context,omitempty"`  // plan: heading path; mockup: enclosing dialog, form, or section
	Route    string   `json:"route,omitempty"`    // mockup: ?query#hash of the page
	Selector string   `json:"selector,omitempty"` // mockup: CSS path of the element
	Steps    []string `json:"steps,omitempty"`    // mockup: selectors clicked since the page loaded
	Width    int      `json:"width,omitempty"`    // mockup: viewport in CSS pixels
	Height   int      `json:"height,omitempty"`
	Theme    string   `json:"theme,omitempty"` // mockup: light or dark
}

func (a *Anchor) plan() bool { return a != nil && a.Line > 0 }

func (a *Anchor) page() bool {
	return a != nil && (a.Route != "" || a.Selector != "" || len(a.Steps) > 0 || a.Width != 0 || a.Height != 0 || a.Theme != "")
}

// NewComment is a comment as a page sends it.
type NewComment struct {
	Path    string   `json:"path"`
	Version int      `json:"version"`
	X       *float64 `json:"x"`
	Y       *float64 `json:"y"`
	Anchor  *Anchor  `json:"anchor"`
	Text    string   `json:"text"`
}

// check validates a new comment against the files of its version.
func (c NewComment) check(v Version) error {
	found := false
	for _, f := range v.Files {
		found = found || f.Path == c.Path
	}
	if !found {
		return fmt.Errorf("version %d has no file %q", v.N, c.Path)
	}
	if (c.X == nil) != (c.Y == nil) || (c.X != nil && (*c.X < 0 || *c.X > 1 || *c.Y < 0 || *c.Y > 1)) {
		return errors.New("a pin needs x and y between 0 and 1")
	}
	a := c.Anchor
	switch {
	case a == nil:
		return nil
	case a.plan():
		doc := false
		for _, f := range renderedDocs(v.Files) {
			doc = doc || f.Path == c.Path
		}
		if !doc {
			return errors.New("source lines need a Markdown document shown on the page")
		}
		if a.EndLine == 0 {
			a.EndLine = a.Line
		}
		if a.EndLine < a.Line || a.EndLine > 1_000_000 {
			return errors.New("invalid source lines")
		}
		if c.X != nil || a.page() {
			return errors.New("a comment on a plan has no pin and no page")
		}
	case a.page():
		if ext := strings.ToLower(path.Ext(c.Path)); (ext != ".html" && ext != ".htm") || !reviewable(v) {
			return errors.New("a page anchor needs an HTML page of a mockup")
		}
		if a.Width < 100 || a.Width > 10000 || a.Height < 100 || a.Height > 10000 {
			return errors.New("a page anchor needs the viewport size")
		}
		if a.Theme != "light" && a.Theme != "dark" {
			return errors.New("a page anchor needs the theme, light or dark")
		}
		if (a.Selector != "") != (c.X != nil) {
			return errors.New("an element needs a pin, and a pin on a page needs an element")
		}
		if a.Route != "" && !strings.HasPrefix(a.Route, "?") && !strings.HasPrefix(a.Route, "#") {
			return errors.New("a route starts with ? or #")
		}
		if len(a.Steps) > 20 {
			return errors.New("at most 20 steps")
		}
	default:
		return errors.New("an anchor needs source lines or a page")
	}
	type field struct {
		name, value string
		max         int
		multiline   bool
	}
	fields := []field{{"quote", a.Quote, 1000, true}, {"context", a.Context, 300, false}, {"route", a.Route, 500, false}, {"selector", a.Selector, 500, false}}
	for _, s := range a.Steps {
		fields = append(fields, field{"step", s, 500, false})
	}
	for _, f := range fields {
		if utf8.RuneCountInString(f.value) > f.max {
			return fmt.Errorf("%s is longer than %d characters", f.name, f.max)
		}
		if err := checkChars(f.value, f.multiline); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return nil
}

// checkChars rejects what would act on a terminal or reorder text there:
// control characters (except line breaks and tabs where allowed) and bidi
// embeddings, overrides, and isolates.
func checkChars(s string, multiline bool) error {
	bad := strings.ContainsFunc(s, func(r rune) bool {
		if r == '\n' || r == '\t' {
			return !multiline
		}
		return unicode.IsControl(r) || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩')
	})
	if bad {
		return errors.New("control or text direction characters are not allowed")
	}
	return nil
}
