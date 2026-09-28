package artifacts

import (
	"bufio"
	"bytes"
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
// point given as fractions of the image (or, with a page anchor, of the
// element) and optionally anchored inside the file. Number counts the
// comments of that file and version, starting at 1, as shown on the page.
type Comment struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Version  int       `json:"version"`
	Number   int       `json:"number"`
	X        *float64  `json:"x,omitempty"`
	Y        *float64  `json:"y,omitempty"`
	Anchor   *Anchor   `json:"anchor,omitempty"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	Resolved bool      `json:"resolved"`
}

// commentEvent is one line of comments.jsonl: a comment, a change of its
// resolved state, or the place of a deleted comment. New events are appended;
// only deleting rewrites the file. It is removed with the artifact.
type commentEvent struct {
	Type     string    `json:"type"` // comment, resolve, or deleted
	ID       string    `json:"id"`
	Path     string    `json:"path,omitempty"`
	Version  int       `json:"version,omitempty"`
	X        *float64  `json:"x,omitempty"`
	Y        *float64  `json:"y,omitempty"`
	Anchor   *Anchor   `json:"anchor,omitempty"`
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
			comments = append(comments, Comment{ID: e.ID, Path: e.Path, Version: e.Version, Number: numbers[key], X: e.X, Y: e.Y, Anchor: e.Anchor, Text: e.Text, At: e.At})
		case "deleted": // keeps its number, so the others keep theirs
			numbers[fmt.Sprintf("%d/%s", e.Version, e.Path)]++
		case "resolve":
			if i, ok := index[e.ID]; ok {
				comments[i].Resolved = e.Resolved
			}
		}
	}
	return comments, scanner.Err()
}

// AddComment records feedback on a published file.
func (s Store) AddComment(p, n string, in NewComment) (Comment, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" || utf8.RuneCountInString(text) > maxCommentRunes {
		return Comment{}, fmt.Errorf("a comment needs 1 to %d characters", maxCommentRunes)
	}
	// Agents read comments in a terminal, where control characters would
	// act as escape sequences.
	if err := checkChars(text, true); err != nil {
		return Comment{}, fmt.Errorf("a comment: %w", err)
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
	var version *Version
	for i := range m.Versions {
		if m.Versions[i].N == in.Version {
			version = &m.Versions[i]
		}
	}
	if version == nil {
		return Comment{}, fmt.Errorf("%s/%s has no version %d", p, n, in.Version)
	}
	if err = in.check(*version); err != nil {
		return Comment{}, fmt.Errorf("%s/%s: %w", p, n, err)
	}
	id := make([]byte, 5)
	_, _ = rand.Read(id)
	e := commentEvent{Type: "comment", ID: hex.EncodeToString(id), Path: in.Path, Version: in.Version, X: in.X, Y: in.Y, Anchor: in.Anchor, Text: text, At: s.now()}
	if err = s.appendComment(p, n, e, maxCommentsFile); err != nil {
		return Comment{}, err
	}
	comments, err := s.Comments(p, n)
	if err != nil {
		return Comment{}, err
	}
	for _, c := range comments {
		if c.ID == e.ID {
			return c, nil
		}
	}
	return Comment{}, errors.New("the comment was not saved")
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
		// Resolving stays possible for a while after comments hit their limit.
		if err = s.appendComment(p, n, commentEvent{Type: "resolve", ID: id, Resolved: resolved, At: s.now()}, 2*maxCommentsFile); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes comments for good. Each one leaves a "deleted" line with only
// its file and version, so the other comments keep their numbers and new ones
// never reuse it; its text and resolve events are gone.
func (s Store) Delete(p, n string, ids []string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err = s.readMeta(p, n); err != nil {
		return fmt.Errorf("artifact %s/%s does not exist", p, n)
	}
	path := s.commentsPath(p, n)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	drop, found := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		var e commentEvent
		if json.Unmarshal(line, &e) != nil {
			continue // empty or torn
		}
		if drop[e.ID] {
			if e.Type != "comment" {
				continue
			}
			found[e.ID] = true
			if line, err = json.Marshal(commentEvent{Type: "deleted", ID: e.ID, Path: e.Path, Version: e.Version, At: s.now()}); err != nil {
				return err
			}
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	for _, id := range ids {
		if !found[id] {
			return fmt.Errorf("%s/%s has no comment %q", p, n, id)
		}
	}
	return writeFileAtomic(path, out.Bytes())
}

// appendComment writes one event unless the log is larger than limit. Callers
// hold the store lock, so a concurrent unpublish cannot move the artifact
// meanwhile.
func (s Store) appendComment(p, n string, e commentEvent, limit int64) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.commentsPath(p, n), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if info.Size() > limit {
		f.Close()
		return fmt.Errorf("%s/%s has too many comments; unpublish it or publish under a new name", p, n)
	}
	// A crash can leave a torn last line; the new event starts on its own.
	last := []byte{'\n'}
	if info.Size() > 0 {
		if _, err = f.ReadAt(last, info.Size()-1); err != nil {
			f.Close()
			return err
		}
	}
	if last[0] != '\n' {
		line = append([]byte{'\n'}, line...)
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
