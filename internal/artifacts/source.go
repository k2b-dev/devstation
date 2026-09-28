package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// SourceLine is one line of a published text file.
type SourceLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

const maxSourceLines, maxSourceRunes = 8, 160

// Source returns lines from to to of a published file, as a plan comment
// shows what it points at: blank lines at the edges trimmed, at most 8 lines
// of 160 characters, control characters replaced. more counts the lines left
// out.
func (s Store) Source(p, n string, version int, file string, from, to int) (lines []SourceLine, more int, err error) {
	m, err := s.readMeta(p, n)
	if err != nil {
		return nil, 0, err
	}
	found := false
	for _, v := range m.Versions {
		for _, f := range v.Files {
			found = found || (v.N == version && f.Path == file)
		}
	}
	if !found {
		return nil, 0, fmt.Errorf("version %d of %s/%s has no file %q", version, p, n, file)
	}
	data, err := os.ReadFile(filepath.Join(s.versionDir(p, n, version), filepath.FromSlash(file)))
	if err != nil {
		return nil, 0, err
	}
	all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	from, to = max(from, 1), min(to, len(all))
	for from <= to && strings.TrimSpace(all[from-1]) == "" {
		from++
	}
	for to >= from && strings.TrimSpace(all[to-1]) == "" {
		to--
	}
	for i := from; i <= to; i++ {
		if len(lines) == maxSourceLines {
			return lines, to - i + 1, nil
		}
		text := []rune(strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') {
				return '?'
			}
			return r
		}, all[i-1]))
		if len(text) > maxSourceRunes {
			text = append(text[:maxSourceRunes-1], '…')
		}
		lines = append(lines, SourceLine{i, string(text)})
	}
	return lines, 0, nil
}
