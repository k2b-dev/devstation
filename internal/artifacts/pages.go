package artifacts

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed templates
var templateFS embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"css":      func() template.CSS { return template.CSS(mustRead("templates/style.css")) },
	"lightbox": func() template.JS { return template.JS(mustRead("templates/lightbox.js")) },
}).ParseFS(templateFS, "templates/pages.html"))

func mustRead(name string) string {
	data, err := templateFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

type crumb struct{ Label, Href string }

type life struct {
	Keep    bool
	Expires time.Time
}

type navLink struct {
	N       int
	Href    string
	Current bool
}

type fileLink struct{ Name, Href, Size string }

type cellView struct {
	Missing       bool
	ID, Name      string
	Row, Col      string
	Section       string
	Href          string
	Video         bool
	Width, Height int
	Attach        []fileLink
}

type rowView struct {
	Label string
	Cells []cellView
}

type sectionView struct {
	ID, Dir string
	Columns []string
	Grid    template.CSS
	Rows    []rowView
	Plain   []cellView
	Other   []fileLink
}

type docView struct {
	ID   string
	HTML template.HTML
}

type versionPage struct {
	Title, Heading, Project, Name string
	Crumbs                        []crumb
	Version, Prev                 int
	LatestHref, CompareHref       string
	At                            time.Time
	IsCurrent                     bool
	Link                          string
	Versions                      []navLink
	Docs                          []docView
	MultiDoc                      bool
	Sections                      []sectionView
	HasMedia                      bool
}

func heading(m Meta) string {
	if m.Title != "" {
		return m.Title
	}
	return m.Name
}

// versionPlaceholder stands for the version root in documents rendered for
// the stable page before the version number is known.
const versionPlaceholder = "v/00version00/"

// docSet holds the Markdown of one version, rendered once for the version
// page and once for the stable page. Rendering happens before the store lock.
type docSet struct {
	version, current []docView
	skip             map[string]bool // rendered documents and the images they embed
	warnings         []string
}

func prepareDocs(files []File, payload string) (docSet, error) {
	ds := docSet{skip: map[string]bool{}}
	published := map[string]bool{}
	for _, f := range files {
		published[f.Path] = true
	}
	var inputs []parsedDocInput
	for _, f := range renderedDocs(files) {
		src, err := os.ReadFile(filepath.Join(payload, filepath.FromSlash(f.Path)))
		if err != nil {
			return ds, err
		}
		inputs = append(inputs, parsedDocInput{f.Path, src})
		ds.skip[f.Path] = true
	}
	own, embedded, warnings := renderDocs(inputs, "./", published)
	stable, _, _ := renderDocs(inputs, versionPlaceholder, published)
	for i, in := range inputs {
		// goldmark safe mode drops raw HTML and dangerous links.
		ds.version = append(ds.version, docView{in.path, template.HTML(own[i])})
		ds.current = append(ds.current, docView{in.path, template.HTML(stable[i])})
	}
	for img := range embedded {
		ds.skip[img] = true
	}
	ds.warnings = warnings
	return ds, nil
}

// renderVersion renders version k of m. From the stable page (current) the
// version root is "v/<k>/"; from the version page itself it is "./".
func renderVersion(m Meta, k int, current bool, ds docSet) ([]byte, error) {
	v := m.Versions[indexOf(m, k)]
	up := "../../" // version page: <artifact>/v/<k>/
	base := "./"
	docs := ds.version
	if current {
		up = "./"
		base = fmt.Sprintf("v/%d/", k)
		docs = nil
		for _, d := range ds.current {
			docs = append(docs, docView{d.ID, template.HTML(strings.ReplaceAll(string(d.HTML), versionPlaceholder, base))})
		}
	}
	p := versionPage{
		Title:   heading(m) + " · " + m.Project,
		Heading: heading(m), Project: m.Project, Name: m.Name,
		Crumbs:  []crumb{{"artifacts", up + "../../"}, {m.Project, up + "../"}, {m.Name, ""}},
		Version: k, At: v.At, IsCurrent: current,
		LatestHref: up, Link: m.Link, Docs: docs, MultiDoc: len(docs) > 1,
	}
	if !current {
		p.Crumbs[2].Href = up
		p.Crumbs = append(p.Crumbs, crumb{fmt.Sprintf("v%d", k), ""})
	}
	for i, other := range m.Versions {
		p.Versions = append(p.Versions, navLink{other.N, fmt.Sprintf("%sv/%d/", up, other.N), other.N == k})
		if other.N == k && i > 0 {
			p.Prev = m.Versions[i-1].N
			p.CompareHref = fmt.Sprintf("%scompare/%d-%d/", up, p.Prev, k)
		}
	}
	for _, s := range layout(v.Files, ds.skip) {
		sv := sectionFor(s, base)
		p.HasMedia = p.HasMedia || len(sv.Rows) > 0 || len(sv.Plain) > 0
		p.Sections = append(p.Sections, sv)
	}
	var buf bytes.Buffer
	err := pages.ExecuteTemplate(&buf, "version", p)
	return buf.Bytes(), err
}

func sectionFor(s section, base string) sectionView {
	id := s.Dir + "/"
	if s.Dir == "" {
		id = "files"
	}
	out := sectionView{ID: id, Dir: s.Dir}
	tracks := []string{"var(--label)"}
	for _, c := range s.Columns {
		label := fmt.Sprint(c.Width)
		if c.Theme != "" {
			label = c.Theme + " · " + label
		}
		out.Columns = append(out.Columns, label)
		// Narrow phone shots get narrow columns; desktop shots stay readable.
		scale := float64(c.Width) / 1440
		scale = max(0.4, min(scale, 1))
		tracks = append(tracks, fmt.Sprintf("minmax(calc(%.2f * var(--col)), %.2ffr)", scale, scale))
	}
	out.Grid = template.CSS("grid-template-columns: " + strings.Join(tracks, " "))
	for _, r := range s.Rows {
		rv := rowView{Label: r.Label}
		for i, c := range r.Cells {
			cv := cellFor(c, base)
			cv.Row, cv.Col, cv.Section = r.Label, out.Columns[i], s.Dir
			rv.Cells = append(rv.Cells, cv)
		}
		out.Rows = append(out.Rows, rv)
	}
	for _, c := range s.Plain {
		cv := cellFor(c, base)
		cv.Section = s.Dir
		out.Plain = append(out.Plain, cv)
	}
	for _, f := range s.Other {
		out.Other = append(out.Other, linkFor(f, base))
	}
	return out
}

func cellFor(c cell, base string) cellView {
	if c.Missing {
		return cellView{Missing: true}
	}
	v := cellView{ID: c.File.Path, Name: path.Base(c.File.Path), Href: base + escapePath(c.File.Path), Video: isVideo(c.File.Path), Width: c.File.Width, Height: c.File.Height}
	for _, a := range c.Attach {
		l := linkFor(a, base)
		l.Name = strings.TrimPrefix(path.Ext(a.Path), ".")
		v.Attach = append(v.Attach, l)
	}
	return v
}

func linkFor(f File, base string) fileLink {
	return fileLink{Name: path.Base(f.Path), Href: base + escapePath(f.Path), Size: humanSize(f.Size)}
}

type redirectPage struct{ Title, Target string }

func renderRedirect(title, target string) ([]byte, error) {
	var buf bytes.Buffer
	err := pages.ExecuteTemplate(&buf, "redirect", redirectPage{title, target})
	return buf.Bytes(), err
}

type pairView struct {
	Path, ID, AHref, BHref string
	Image, Slider          bool
	Width, Height          int
}

type comparePage struct {
	Title, Heading, Project, Name string
	Crumbs                        []crumb
	A, B                          int
	AHref, BHref                  string
	Changed                       []pairView
	Added, Removed                []fileLink
	Unchanged                     int
}

// renderCompare lives at <artifact>/compare/<a>-<b>/.
func renderCompare(m Meta, a, b int) ([]byte, error) {
	va, vb := m.Versions[indexOf(m, a)], m.Versions[indexOf(m, b)]
	up := "../../"
	baseA, baseB := fmt.Sprintf("%sv/%d/", up, a), fmt.Sprintf("%sv/%d/", up, b)
	p := comparePage{
		Title: heading(m) + " · v" + fmt.Sprint(a) + " → v" + fmt.Sprint(b), Heading: heading(m),
		Project: m.Project, Name: m.Name, A: a, B: b, AHref: baseA, BHref: baseB,
		Crumbs: []crumb{{"artifacts", up + "../../"}, {m.Project, up + "../"}, {m.Name, up}, {fmt.Sprintf("v%d → v%d", a, b), ""}},
	}
	old := map[string]File{}
	for _, f := range va.Files {
		old[f.Path] = f
	}
	for _, f := range vb.Files {
		prev, ok := old[f.Path]
		delete(old, f.Path)
		switch {
		case !ok:
			p.Added = append(p.Added, linkFor(f, baseB))
		case prev.SHA256 == f.SHA256:
			p.Unchanged++
		default:
			pv := pairView{Path: f.Path, ID: f.Path, AHref: baseA + escapePath(f.Path), BHref: baseB + escapePath(f.Path), Image: isImage(f.Path)}
			pv.Slider = pv.Image && f.Width > 0 && f.Width == prev.Width && f.Height == prev.Height
			pv.Width, pv.Height = f.Width, f.Height
			p.Changed = append(p.Changed, pv)
		}
	}
	for _, f := range va.Files {
		if _, removed := old[f.Path]; removed {
			p.Removed = append(p.Removed, linkFor(f, baseA))
		}
	}
	sort.SliceStable(p.Changed, func(i, j int) bool {
		if p.Changed[i].Image != p.Changed[j].Image {
			return p.Changed[i].Image
		}
		return naturalLess(p.Changed[i].Path, p.Changed[j].Path)
	})
	var buf bytes.Buffer
	err := pages.ExecuteTemplate(&buf, "compare", p)
	return buf.Bytes(), err
}

type card struct {
	Href, Heading, Name, Summary, Size, Link string
	Versions                                 int
	Updated                                  time.Time
	Life                                     life
}

type projectPage struct {
	Title, Project string
	Crumbs         []crumb
	Cards          []card
}

func renderProject(p string, metas []Meta) ([]byte, error) {
	page := projectPage{Title: p + " · artifacts", Project: p, Crumbs: []crumb{{"artifacts", "../"}, {p, ""}}}
	for _, m := range metas {
		v := m.latest()
		page.Cards = append(page.Cards, card{
			Href: "./" + m.Name + "/", Heading: heading(m), Name: m.Name, Summary: summary(v),
			Size: humanSize(v.Bytes), Link: m.Link, Versions: v.N, Updated: m.Updated, Life: life{m.Keep, m.Expires()},
		})
	}
	var buf bytes.Buffer
	err := pages.ExecuteTemplate(&buf, "project", page)
	return buf.Bytes(), err
}

type projectEntry struct {
	Name, Href string
	Count      int
	Updated    time.Time
}

type rootPage struct {
	Title    string
	Crumbs   []crumb
	Projects []projectEntry
}

func renderRoot(projects []projectEntry) ([]byte, error) {
	var buf bytes.Buffer
	err := pages.ExecuteTemplate(&buf, "root", rootPage{Title: "artifacts", Crumbs: []crumb{{"artifacts", ""}}, Projects: projects})
	return buf.Bytes(), err
}

func summary(v Version) string {
	if v.Kind == "site" {
		return "site"
	}
	var images, videos, docs, other int
	for _, f := range v.Files {
		switch {
		case isImage(f.Path):
			images++
		case isVideo(f.Path):
			videos++
		case isMarkdown(f.Path):
			docs++
		default:
			other++
		}
	}
	var parts []string
	for _, c := range []struct {
		n    int
		unit string
	}{{docs, "doc"}, {images, "image"}, {videos, "video"}, {other, "file"}} {
		if c.n == 1 {
			parts = append(parts, "1 "+c.unit)
		} else if c.n > 1 {
			parts = append(parts, fmt.Sprintf("%d %ss", c.n, c.unit))
		}
	}
	return strings.Join(parts, " · ")
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func indexOf(m Meta, k int) int {
	for i, v := range m.Versions {
		if v.N == k {
			return i
		}
	}
	panic(fmt.Sprintf("version %d missing from %s/%s", k, m.Project, m.Name))
}
