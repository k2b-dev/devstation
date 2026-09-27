package artifacts

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxCommentRunes = 4000
	maxCommentsFile = 1 << 20
)

// Comment is feedback on one file of one version, optionally pinned to a
// point given as fractions of the image width and height. Number counts the
// comments of that file and version, starting at 1, as shown on the image.
type Comment struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Version  int       `json:"version"`
	Number   int       `json:"number"`
	X        *float64  `json:"x,omitempty"`
	Y        *float64  `json:"y,omitempty"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	Resolved bool      `json:"resolved"`
}

// commentEvent is one line of comments.jsonl: a comment, or a change of its
// resolved state. The log is append-only and removed with the artifact.
type commentEvent struct {
	Type     string    `json:"type"` // comment or resolve
	ID       string    `json:"id"`
	Path     string    `json:"path,omitempty"`
	Version  int       `json:"version,omitempty"`
	X        *float64  `json:"x,omitempty"`
	Y        *float64  `json:"y,omitempty"`
	Text     string    `json:"text,omitempty"`
	Resolved bool      `json:"resolved,omitempty"`
	At       time.Time `json:"at"`
}

func (s Store) commentsPath(p, n string) string {
	return filepath.Join(s.artifact(p, n), "comments.jsonl")
}

// Comments returns the comments of an artifact in the order they were made.
func (s Store) Comments(p, n string) ([]Comment, error) {
	if _, err := s.readMeta(p, n); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("artifact %s/%s does not exist", p, n)
		}
		return nil, err
	}
	f, err := os.Open(s.commentsPath(p, n))
	if errors.Is(err, os.ErrNotExist) {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	comments := []Comment{}
	index := map[string]int{}
	numbers := map[string]int{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), maxCommentsFile)
	for scanner.Scan() {
		var e commentEvent
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue // a torn last line from a crash; the rest stays usable
		}
		switch e.Type {
		case "comment":
			key := fmt.Sprintf("%d/%s", e.Version, e.Path)
			numbers[key]++
			index[e.ID] = len(comments)
			comments = append(comments, Comment{ID: e.ID, Path: e.Path, Version: e.Version, Number: numbers[key], X: e.X, Y: e.Y, Text: e.Text, At: e.At})
		case "resolve":
			if i, ok := index[e.ID]; ok {
				comments[i].Resolved = e.Resolved
			}
		}
	}
	return comments, scanner.Err()
}

// AddComment records feedback on a published file.
func (s Store) AddComment(p, n, path string, version int, x, y *float64, text string) (Comment, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxCommentRunes {
		return Comment{}, fmt.Errorf("a comment needs 1 to %d characters", maxCommentRunes)
	}
	if (x == nil) != (y == nil) || (x != nil && (*x < 0 || *x > 1 || *y < 0 || *y > 1)) {
		return Comment{}, fmt.Errorf("a pin needs x and y between 0 and 1")
	}
	unlock, err := s.lock()
	if err != nil {
		return Comment{}, err
	}
	defer unlock()
	m, err := s.readMeta(p, n)
	if err != nil {
		return Comment{}, fmt.Errorf("artifact %s/%s does not exist", p, n)
	}
	found := false
	for _, v := range m.Versions {
		if v.N != version {
			continue
		}
		for _, f := range v.Files {
			found = found || f.Path == path
		}
	}
	if !found {
		return Comment{}, fmt.Errorf("version %d of %s/%s has no file %q", version, p, n, path)
	}
	id := make([]byte, 5)
	_, _ = rand.Read(id)
	e := commentEvent{Type: "comment", ID: hex.EncodeToString(id), Path: path, Version: version, X: x, Y: y, Text: text, At: s.now()}
	if err = s.appendComment(p, n, e); err != nil {
		return Comment{}, err
	}
	comments, err := s.Comments(p, n)
	if err != nil {
		return Comment{}, err
	}
	return comments[len(comments)-1], nil
}

// Resolve marks comments as done or open again.
func (s Store) Resolve(p, n string, ids []string, resolved bool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	comments, err := s.Comments(p, n)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, c := range comments {
		known[c.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("%s/%s has no comment %q", p, n, id)
		}
	}
	for _, id := range ids {
		if err = s.appendComment(p, n, commentEvent{Type: "resolve", ID: id, Resolved: resolved, At: s.now()}); err != nil {
			return err
		}
	}
	return nil
}

// appendComment writes one event. Callers hold the store lock, so a
// concurrent unpublish cannot move the artifact meanwhile.
func (s Store) appendComment(p, n string, e commentEvent) error {
	path := s.commentsPath(p, n)
	if info, err := os.Stat(path); err == nil && info.Size() > maxCommentsFile {
		return fmt.Errorf("%s/%s has too many comments; publish a new version or name", p, n)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// OpenComments counts comments that are not resolved.
func OpenComments(comments []Comment) int {
	open := 0
	for _, c := range comments {
		if !c.Resolved {
			open++
		}
	}
	return open
}
