package artifacts

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchorValidation(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "plan.md"), []byte("# Plan\n\nOne\ntwo\n"))
	write(t, filepath.Join(src, "a-light-1440.png"), pngBytes(t, 20, 10, 50))
	write(t, filepath.Join(src, "report.html"), []byte("<p>attached</p>"))
	if _, err := s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}}); err != nil {
		t.Fatal(err)
	}
	mock := t.TempDir()
	write(t, filepath.Join(mock, "page.html"), []byte("<button>Save</button>"))
	if _, err := s.Publish(Options{Project: "p", Name: "m", Paths: []string{mock}}); err != nil {
		t.Fatal(err)
	}
	half := fp(0.5)
	view := func(a Anchor) *Anchor { a.Width, a.Height, a.Theme = 390, 844, "dark"; return &a }
	good := []NewComment{
		{Path: "plan.md", Anchor: &Anchor{Line: 3, EndLine: 4, Quote: "One\ntwo", Context: "Plan"}},
		{Path: "plan.md", Anchor: &Anchor{Line: 3}},
		{Path: "page.html", X: half, Y: half, Anchor: view(Anchor{Route: "#billing", Selector: "dialog#x > button", Quote: "Save", Context: `dialog "Invite"`, Steps: []string{"button#open"}})},
		{Path: "page.html", Anchor: view(Anchor{})},
		{Path: "a-light-1440.png", X: half, Y: half},
	}
	for i, c := range good {
		c.Version, c.Text = 1, "ok"
		name := "n"
		if c.Path == "page.html" {
			name = "m"
		}
		if _, err := s.AddComment("p", name, c); err != nil {
			t.Errorf("good %d rejected: %v", i, err)
		}
	}
	if cs, _ := s.Comments("p", "n"); cs[1].Anchor.EndLine != 3 {
		t.Fatalf("plan anchor not stored: %+v", cs[1].Anchor)
	}
	if cs, _ := s.Comments("p", "m"); cs[0].Anchor.Steps[0] != "button#open" {
		t.Fatalf("page anchor not stored: %+v", cs[0].Anchor)
	}
	bad := map[string]NewComment{
		"lines on an image":      {Path: "a-light-1440.png", Anchor: &Anchor{Line: 1}},
		"lines backwards":        {Path: "plan.md", Anchor: &Anchor{Line: 4, EndLine: 3}},
		"pin on a plan":          {Path: "plan.md", X: half, Y: half, Anchor: &Anchor{Line: 3}},
		"page fields on a plan":  {Path: "plan.md", Anchor: &Anchor{Line: 3, Width: 390, Height: 844, Theme: "dark"}},
		"page on an image":       {Path: "a-light-1440.png", Anchor: view(Anchor{})},
		"page outside a mockup":  {Path: "report.html", Anchor: view(Anchor{})},
		"no viewport":            {Path: "page.html", Anchor: &Anchor{Route: "#x", Theme: "dark"}},
		"odd theme":              {Path: "page.html", Anchor: &Anchor{Width: 390, Height: 844, Theme: "blue"}},
		"route without prefix":   {Path: "page.html", Anchor: view(Anchor{Route: "billing"})},
		"selector without pin":   {Path: "page.html", Anchor: view(Anchor{Selector: "button"})},
		"pin without selector":   {Path: "page.html", X: half, Y: half, Anchor: view(Anchor{})},
		"too many steps":         {Path: "page.html", Anchor: view(Anchor{Steps: strings.Split(strings.Repeat("a,", 21), ",")})},
		"long quote":             {Path: "plan.md", Anchor: &Anchor{Line: 3, Quote: strings.Repeat("q", 1001)}},
		"escape in selector":     {Path: "page.html", X: half, Y: half, Anchor: view(Anchor{Selector: "button\x1b[2J"})},
		"newline in context":     {Path: "plan.md", Anchor: &Anchor{Line: 3, Context: "a\nb"}},
		"quote without position": {Path: "plan.md", Anchor: &Anchor{Quote: "One"}},
		"bidi in text":           {Path: "plan.md", Text: "fine‮enif"},
	}
	for name, c := range bad {
		c.Version = 1
		if c.Text == "" {
			c.Text = "x"
		}
		artifact := "n"
		if c.Path == "page.html" {
			artifact = "m"
		}
		if _, err := s.AddComment("p", artifact, c); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
}
