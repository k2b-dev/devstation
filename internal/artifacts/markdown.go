package artifacts

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Safe mode: goldmark omits raw HTML and filters dangerous link schemes.
// HTML that should run belongs in a .html file (site mode).
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// headingIDs gives heading anchors that follow GitHub's rules within each
// document and stay unique across all documents of one page. byDoc maps each
// document's own GitHub anchors to the page-wide IDs, so links such as
// other.md#overview land in the right document.
type headingIDs struct {
	used  map[string]bool // page-wide
	byDoc map[string]map[string]string
}

func newHeadingIDs(docs []parsedDocInput) *headingIDs {
	// Fixed ids of the page: the root gallery section, the theme button, and the viewer.
	h := &headingIDs{used: map[string]bool{"files": true, "theme": true, "lightbox": true}, byDoc: map[string]map[string]string{}}
	for _, d := range docs {
		h.used[d.path] = true
	}
	return h
}

// assign sets the id attribute of every heading in one document.
func (h *headingIDs) assign(doc string, root ast.Node, src []byte) {
	local := map[string]bool{} // GitHub's anchors within this document
	h.byDoc[doc] = map[string]string{}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		heading, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		anchor := uniqueID(local, githubSlug(plainText(heading, src)))
		h.byDoc[doc][anchor] = uniqueID(h.used, anchor)
		heading.SetAttributeString("id", []byte(h.byDoc[doc][anchor]))
		return ast.WalkSkipChildren, nil
	})
}

// uniqueID returns name, or name-1, name-2, … whichever is still unused, and
// marks it used, as GitHub does for repeated headings.
func uniqueID(used map[string]bool, name string) string {
	id := name
	for i := 1; used[id]; i++ {
		id = fmt.Sprintf("%s-%d", name, i)
	}
	used[id] = true
	return id
}

// plainText is the visible text of a heading: emphasis, code and link text
// count; inline HTML tags do not.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// githubSlug lower-cases the text, keeps letters, marks, digits, connector
// punctuation and hyphens, and turns spaces into hyphens.
func githubSlug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || unicode.Is(unicode.Pc, r) || r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "section"
	}
	return b.String()
}

type parsedDoc struct {
	path string
	src  []byte
	root ast.Node
}

// renderDocs renders the documents of one page. Relative links and images are
// rewritten to base (the version root as seen from the page); links to other
// rendered documents and in-document fragments become page anchors. It
// returns the HTML per document, the embedded image paths and warnings about
// targets that were not published.
func renderDocs(docs []parsedDocInput, base string, published map[string]bool) ([]string, map[string]bool, []string) {
	ids := newHeadingIDs(docs)
	var parsed []parsedDoc
	for _, d := range docs {
		root := md.Parser().Parse(text.NewReader(d.src))
		ids.assign(d.path, root, d.src)
		parsed = append(parsed, parsedDoc{d.path, d.src, root})
	}
	anchor := func(doc, fragment string) string {
		if id, ok := ids.byDoc[doc][fragment]; ok {
			return "#" + url.PathEscape(id)
		}
		return "#" + url.PathEscape(fragment)
	}
	embedded := map[string]bool{}
	var warnings []string
	var out []string
	for _, d := range parsed {
		_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			var dest *[]byte
			switch node := n.(type) {
			case *ast.Link:
				dest = &node.Destination
			case *ast.Image:
				dest = &node.Destination
			default:
				return ast.WalkContinue, nil
			}
			if frag, ok := strings.CutPrefix(string(*dest), "#"); ok {
				if f, err := url.PathUnescape(frag); err == nil {
					*dest = []byte(anchor(d.path, f))
				}
				return ast.WalkContinue, nil
			}
			target, fragment, ok := resolve(string(*dest), d.path)
			if !ok {
				return ast.WalkContinue, nil
			}
			if !published[target] {
				warnings = append(warnings, fmt.Sprintf("%s: %s is not part of this publish", d.path, target))
				return ast.WalkContinue, nil
			}
			if _, isImage := n.(*ast.Image); isImage {
				embedded[target] = true
			}
			switch {
			case ids.byDoc[target] != nil && fragment == "":
				*dest = []byte("#" + url.PathEscape(target))
			case ids.byDoc[target] != nil:
				*dest = []byte(anchor(target, fragment))
			default:
				*dest = []byte(base + escapePath(target) + fragmentSuffix(fragment))
			}
			return ast.WalkContinue, nil
		})
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, d.src, d.root); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", d.path, err))
		}
		out = append(out, buf.String())
	}
	return out, embedded, warnings
}

type parsedDocInput struct {
	path string
	src  []byte
}

// resolve maps a relative link inside the payload to its slash path.
// Absolute URLs, root paths, pure fragments and links leaving the payload are
// left alone.
func resolve(dest, from string) (target, fragment string, ok bool) {
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") || u.RawQuery != "" {
		return "", "", false
	}
	target = path.Clean(path.Join(path.Dir(from), u.Path))
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", "", false
	}
	return target, u.Fragment, true
}

func fragmentSuffix(f string) string {
	if f == "" {
		return ""
	}
	return "#" + url.PathEscape(f)
}

// escapePath escapes each segment so names like "a?b#c%.png" stay one path.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
