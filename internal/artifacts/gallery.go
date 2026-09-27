package artifacts

import (
	"path"
	"sort"
	"strconv"
	"strings"
)

var themeRank = map[string]int{"light": 1, "hell": 1, "dark": 2, "dunkel": 2}

// shot is a media file name split into a gallery row and column.
// Grammar: the last theme token (light, dark, hell, dunkel) and a 3–4 digit
// width directly after it (else directly before it) form the column; with no
// theme, a trailing width does. All other tokens form the row, so variants
// such as "-crop" become rows of their own.
type shot struct {
	row   string
	width int
	theme string
}

func parseShot(name string) (shot, bool) {
	stem := strings.TrimSuffix(path.Base(name), path.Ext(name))
	tokens := strings.Split(stem, "-")
	ti, wi := -1, -1
	for i := len(tokens) - 1; i >= 0; i-- {
		if themeRank[strings.ToLower(tokens[i])] > 0 {
			ti = i
			break
		}
	}
	switch {
	case ti >= 0 && ti+1 < len(tokens) && isWidth(tokens[ti+1]):
		wi = ti + 1
	case ti > 0 && isWidth(tokens[ti-1]):
		wi = ti - 1
	case ti < 0 && isWidth(tokens[len(tokens)-1]):
		wi = len(tokens) - 1
	}
	if wi < 0 {
		return shot{}, false
	}
	var s shot
	s.width, _ = strconv.Atoi(tokens[wi])
	var row []string
	for i, t := range tokens {
		switch i {
		case ti:
			s.theme = strings.ToLower(t)
		case wi:
		default:
			row = append(row, t)
		}
	}
	s.row = strings.Join(row, "-")
	return s, true
}

func isWidth(t string) bool {
	if len(t) < 3 || len(t) > 4 {
		return false
	}
	n, err := strconv.Atoi(t)
	return err == nil && n >= 100 && t[0] != '0'
}

type column struct {
	Width int
	Theme string
}

type cell struct {
	File    File
	Attach  []File // same-stem non-media files, e.g. x.html next to x.png
	Missing bool
}

type row struct {
	Label string
	Cells []cell
}

type section struct {
	Dir     string // "" for the version root
	Columns []column
	Rows    []row
	Plain   []cell // media without a width
	Other   []File // everything else that is not rendered as Markdown
}

// layout groups the files of one version into gallery sections per directory.
// Files in skip (rendered Markdown and the images it embeds) are left out.
func layout(files []File, skip map[string]bool) []section {
	byDir := map[string][]File{}
	for _, f := range files {
		if skip[f.Path] {
			continue
		}
		dir := path.Dir(f.Path)
		if dir == "." {
			dir = ""
		}
		byDir[dir] = append(byDir[dir], f)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return naturalLess(dirs[i], dirs[j]) })
	var sections []section
	for _, dir := range dirs {
		sections = append(sections, layoutDir(dir, byDir[dir]))
	}
	return sections
}

func layoutDir(dir string, files []File) section {
	s := section{Dir: dir}
	stems := map[string]*cell{}
	cells := []*cell{}
	for _, f := range files {
		if isMedia(f.Path) {
			c := &cell{File: f}
			cells = append(cells, c)
			stems[strings.TrimSuffix(f.Path, path.Ext(f.Path))] = c
		}
	}
	for _, f := range files {
		if isMedia(f.Path) {
			continue
		}
		if c := stems[strings.TrimSuffix(f.Path, path.Ext(f.Path))]; c != nil {
			c.Attach = append(c.Attach, f)
		} else {
			s.Other = append(s.Other, f)
		}
	}
	type key struct {
		row string
		col column
	}
	grid := map[key]*cell{}
	rowSet, colSet := map[string]bool{}, map[column]bool{}
	for _, c := range cells {
		sh, ok := parseShot(c.File.Path)
		k := key{sh.row, column{sh.width, sh.theme}}
		if !ok || grid[k] != nil {
			s.Plain = append(s.Plain, *c)
			continue
		}
		grid[k] = c
		rowSet[k.row], colSet[k.col] = true, true
	}
	for c := range colSet {
		s.Columns = append(s.Columns, c)
	}
	sort.Slice(s.Columns, func(i, j int) bool {
		a, b := s.Columns[i], s.Columns[j]
		if a.Width != b.Width {
			return a.Width > b.Width
		}
		return themeRank[a.Theme] < themeRank[b.Theme]
	})
	var rows []string
	for r := range rowSet {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return naturalLess(rows[i], rows[j]) })
	for _, r := range rows {
		out := row{Label: r}
		for _, c := range s.Columns {
			if found := grid[key{r, c}]; found != nil {
				out.Cells = append(out.Cells, *found)
			} else {
				out.Cells = append(out.Cells, cell{Missing: true})
			}
		}
		s.Rows = append(s.Rows, out)
	}
	sort.Slice(s.Plain, func(i, j int) bool { return naturalLess(s.Plain[i].File.Path, s.Plain[j].File.Path) })
	sort.Slice(s.Other, func(i, j int) bool { return naturalLess(s.Other[i].Path, s.Other[j].Path) })
	return s
}

// naturalLess orders embedded numbers by value, so "2-x" sorts before "10-x".
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ca, cb := chunk(a), chunk(b)
		if ca != cb {
			na, ea := strconv.Atoi(ca)
			nb, eb := strconv.Atoi(cb)
			if ea == nil && eb == nil && na != nb {
				return na < nb
			}
			return ca < cb
		}
		a, b = a[len(ca):], b[len(cb):]
	}
	return len(a) < len(b)
}

func chunk(s string) string {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i]
}
