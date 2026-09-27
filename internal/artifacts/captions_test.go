package artifacts

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCaptions(t *testing.T) {
	known := map[string]bool{"01-list": true, "02-dialog": true, "diagram": true}
	src := "01-list: Empty list\r\nNo entries yet;\r\n\"New\" sits top right.\r\n\r\n02-dialog: " + strings.Repeat("t", 70) + "\n" + strings.Repeat("x", 200) + "\n\n\nnot a caption\n\n03-gone: Missing\n\ndiagram:   Flow  \n"
	caps, warnings := parseCaptions("captions.txt", []byte(src), known)
	if c := caps["01-list"]; c.Title != "Empty list" || c.Text != `No entries yet; "New" sits top right.` {
		t.Fatalf("01-list: %+v", c)
	}
	if c := caps["02-dialog"]; len([]rune(c.Title)) != maxCaptionTitle || !strings.HasSuffix(c.Title, "…") || len([]rune(c.Text)) != maxCaptionText {
		t.Fatalf("not shortened: %+v", c)
	}
	if c := caps["diagram"]; c.Title != "Flow" || c.Text != "" {
		t.Fatalf("diagram: %+v", c)
	}
	if len(caps) != 3 || len(warnings) != 4 {
		t.Fatalf("%d captions, warnings %q", len(caps), warnings)
	}
	for _, want := range []string{"title of \"02-dialog\"", "text of \"02-dialog\"", `"not a caption"`, `no image for "03-gone"`} {
		if !strings.Contains(strings.Join(warnings, "\n"), want) {
			t.Errorf("no warning about %s: %q", want, warnings)
		}
	}
}

func TestPublishShowsCaptions(t *testing.T) {
	s, _ := testStore(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "01-list-light-1440.png"), pngBytes(t, 40, 30, 10))
	write(t, filepath.Join(src, "01-list-light-390.png"), pngBytes(t, 20, 30, 10))
	write(t, filepath.Join(src, "diagram.png"), pngBytes(t, 40, 30, 10))
	write(t, filepath.Join(src, "captions.txt"), []byte("01-list: Empty list\nNo <entries> yet.\n\ndiagram: Flow\n\n09-typo: Nothing\n"))
	r, err := s.Publish(Options{Project: "p", Name: "n", Paths: []string{src}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], `no image for "09-typo"`) {
		t.Fatalf("warnings: %q", r.Warnings)
	}
	page := read(t, filepath.Join(s.artifact("p", "n"), "index.html"))
	for _, want := range []string{
		`<span class="cap">Empty list</span><span class="label">01-list</span>`,
		`title="No &lt;entries&gt; yet."`,
		`data-title="Empty list" data-text="No &lt;entries&gt; yet."`,
		`<span class="cap">Flow</span>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, "captions.txt") {
		t.Error("captions.txt is listed as a file")
	}
}
