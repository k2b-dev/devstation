package artifacts

import (
	"strings"
	"testing"
)

func TestSourceLineStamps(t *testing.T) {
	src := "# Title\n\nFirst line\nsecond line.\n\nSetext\n------\n\n- tight\n- item\n  - nested\n\n1. loose\n\n   more\n\n> quoted\n\n```go\nx := 1\ny := 2\n```\n\n    indented\n\n```\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\n<div>raw</div>\n\nText with `<pre><code>` inside.\n"
	out, _, _ := renderDocs([]parsedDocInput{{"plan.md", []byte(src)}}, "./", map[string]bool{"plan.md": true})
	html := out[0]
	for _, want := range []string{
		`<h1 id="title" data-line="1-1">`,
		`<p data-line="3-4">First line`,
		`<h2 id="setext" data-line="6-6">`,
		`<li data-line="9-9">tight`,
		`<li data-line="10-10">item`,
		`<li data-line="11-11">nested`,
		`<li data-line="13-13">` + "\n" + `<p data-line="13-13">loose`,
		`<p data-line="15-15">more`,
		`<blockquote>` + "\n" + `<p data-line="17-17">quoted`,
		`<pre data-line="20-21"><code class="language-go">`,
		`<pre data-line="24-24"><code>indented`,
		`<pre><code></code></pre>`,
		`<thead data-line="29-29">`,
		`<tr data-line="31-31">`,
		`<tr data-line="32-32">`,
		`<!-- raw HTML omitted -->`,
		`<p data-line="36-36">Text with <code>&lt;pre&gt;&lt;code&gt;</code>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %s", want)
		}
	}
	if t.Failed() {
		t.Log(html)
	}
	if got := stampCode("<pre><code>a</code></pre>", []string{"1-1", "2-2"}); got != "<pre><code>a</code></pre>" {
		t.Errorf("stamped despite a count mismatch: %s", got)
	}
}
