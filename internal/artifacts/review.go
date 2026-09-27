package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type reviewPage struct {
	Title, Heading, Project, Name string
	Crumbs                        []crumb
	Version                       int
	Versions                      []navLink
	Raw                           string // the entry page of the mockup
	Root, Base, Entry             string // the artifact and the version, relative to the page
	Pages, Reviews                template.JS
	CommentsHref, CommentsAPI     string
	Docs                          []docView // none; the panel template is shared with plans
	MultiDoc                      bool
	Hint                          string
}

// reviewable reports whether a version is a mockup with HTML pages, which
// get a review page. A single PDF or text file is a "site" too, but has none.
func reviewable(v Version) bool {
	if v.Kind != "site" {
		return false
	}
	for _, f := range v.Files {
		if ext := strings.ToLower(path.Ext(f.Path)); ext == ".html" || ext == ".htm" {
			return true
		}
	}
	return false
}

// renderReview lives at <artifact>/review/<k>/ for mockup ("site") versions:
// it shows the unchanged mockup from v/<k>/ in a frame, with the comment
// panel and comment mode around it. reviews lists the versions that have
// such a page, for the version links.
func renderReview(m Meta, k int, reviews []int) ([]byte, error) {
	v := m.Versions[indexOf(m, k)]
	up := "../../"
	base := fmt.Sprintf("%sv/%d/", up, k)
	html := []string{}
	for _, f := range v.Files {
		if ext := strings.ToLower(path.Ext(f.Path)); ext == ".html" || ext == ".htm" {
			html = append(html, f.Path)
		}
	}
	pagesJSON, err := json.Marshal(html)
	if err != nil {
		return nil, err
	}
	reviewsJSON, err := json.Marshal(reviews)
	if err != nil {
		return nil, err
	}
	p := reviewPage{
		Title: heading(m) + " · " + m.Project, Heading: heading(m), Project: m.Project, Name: m.Name, Version: k,
		Crumbs: []crumb{{"artifacts", up + "../../"}, {m.Project, up + "../"}, {m.Name, up}, {fmt.Sprintf("v%d", k), ""}},
		Raw:    base + escapePath(v.Entry), Root: up, Base: base, Entry: v.Entry,
		Pages: template.JS(pagesJSON), Reviews: template.JS(reviewsJSON),
		CommentsHref: up + "comments.jsonl", CommentsAPI: CommentsPrefix + m.Project + "/" + m.Name,
		Hint: "Press Comment (c) and click an element, or write about this page.",
	}
	for _, other := range m.Versions {
		href := fmt.Sprintf("%sv/%d/", up, other.N)
		for _, r := range reviews {
			if r == other.N {
				href = fmt.Sprintf("../%d/", other.N)
			}
		}
		p.Versions = append(p.Versions, navLink{other.N, href, other.N == k})
	}
	var buf bytes.Buffer
	err = pages.ExecuteTemplate(&buf, "review", p)
	return buf.Bytes(), err
}

// writeReview writes the review page of version k. Pages of earlier versions
// stay as they were, like compare pages.
func (s Store) writeReview(m Meta, k int) error {
	var reviews []int
	for _, v := range m.Versions {
		_, err := os.Stat(filepath.Join(s.artifact(m.Project, m.Name), "review", fmt.Sprint(v.N), "index.html"))
		if v.N == k || err == nil {
			reviews = append(reviews, v.N)
		}
	}
	page, err := renderReview(m, k, reviews)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.artifact(m.Project, m.Name), "review", fmt.Sprint(k))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "index.html"), page)
}
