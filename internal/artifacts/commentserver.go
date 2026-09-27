package artifacts

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// CommentsPrefix is where `dev daemon` serves CommentHandler on the artifacts host.
const CommentsPrefix = "/_devstation/comments/"

// CommentHandler serves the comment API that the artifact pages call, below
// the prefix it is mounted at:
//
//	POST /PROJECT/NAME          {"path", "version", "x", "y", "anchor", "text"}
//	POST /PROJECT/NAME/resolve  {"ids": [...], "resolved": true}
//	POST /PROJECT/NAME/delete   {"ids": [...]}
//
// Pages read comments from the artifact's comments.jsonl. Only same-origin
// JSON requests are accepted, so other sites cannot post through a visitor's
// browser.
func CommentHandler(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if !strings.HasPrefix(r.URL.Path, "/") || len(parts) < 2 || len(parts) > 3 || !label.MatchString(parts[0]) || !label.MatchString(parts[1]) {
			fail(w, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			fail(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		if err := sameOrigin(r); err != nil {
			fail(w, http.StatusForbidden, err.Error())
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		p, n := parts[0], parts[1]
		switch {
		case len(parts) == 2:
			var in NewComment
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				fail(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			c, err := s.AddComment(p, n, in)
			if err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(c)
		case parts[2] == "resolve" || parts[2] == "delete":
			var in struct {
				IDs      []string `json:"ids"`
				Resolved bool     `json:"resolved"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.IDs) == 0 {
				fail(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			var err error
			if parts[2] == "delete" {
				err = s.Delete(p, n, in.IDs)
			} else {
				err = s.Resolve(p, n, in.IDs, in.Resolved)
			}
			if err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			fail(w, http.StatusNotFound, "not found")
		}
	})
}

// sameOrigin requires a JSON body, which browsers only send cross-site after a
// CORS preflight that this server never answers, and an Origin matching Host.
func sameOrigin(r *http.Request) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("send application/json")
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return errors.New("cross-site request")
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" || origin.Host != r.Host {
		return errors.New("origin does not match")
	}
	return nil
}

func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
