package shot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FindBrowser returns the Chromium to use: an explicit path, $DEVSTATION_BROWSER,
// a Chrome or Chromium on PATH, or one that Playwright downloaded.
func FindBrowser(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv("DEVSTATION_BROWSER")
	}
	if explicit != "" {
		p, err := exec.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("browser %q: %w", explicit, err)
		}
		return p, nil
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome", "chrome", "chrome-headless-shell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	cache := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if cache == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cache = filepath.Join(home, ".cache", "ms-playwright")
		}
	}
	// Prefer the headless shell, which needs fewer system libraries, and the
	// highest revision.
	for _, pattern := range []string{"chromium_headless_shell-*/chrome-*/chrome-headless-shell", "chromium-*/chrome-linux*/chrome"} {
		matches, _ := filepath.Glob(filepath.Join(cache, pattern))
		sort.Slice(matches, func(i, j int) bool { return revision(matches[i]) < revision(matches[j]) })
		if len(matches) > 0 {
			return matches[len(matches)-1], nil
		}
	}
	return "", fmt.Errorf("no Chrome or Chromium found; install one, pass --browser PATH, or set DEVSTATION_BROWSER")
}

func revision(path string) int {
	m := revisionPattern.FindStringSubmatch(path)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

var revisionPattern = regexp.MustCompile(`-(\d+)/`)

type browser struct {
	*cdp
	cmd     *exec.Cmd
	dataDir string
	stderr  *tail
}

// tail keeps the last bytes the browser wrote to stderr, to explain a failed start.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (b *browser) stderrTail() string {
	b.stderr.mu.Lock()
	defer b.stderr.mu.Unlock()
	if text := strings.TrimSpace(string(b.stderr.buf)); text != "" {
		return "\n" + text
	}
	return ""
}

// launch starts a headless browser with a private profile that is removed on
// close. resolverRule maps host names to an address, as --host-resolver-rules.
func launch(path, resolverRule string) (*browser, error) {
	dataDir, err := os.MkdirTemp("", "devstation-shot-")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + dataDir,
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--disable-gpu", "--hide-scrollbars", "--mute-audio", "--force-color-profile=srgb",
		// As Playwright does by default: many hosts do not allow the user
		// namespaces the Chromium sandbox needs. Without it, a compromised
		// renderer runs with this user's rights, so shoot only trusted pages.
		"--no-sandbox",
		// Headless Chromium reports no hover and no pointer device, so pages
		// would render their touch variants; report a mouse like a desktop.
		"--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4",
	}
	if !strings.Contains(filepath.Base(path), "headless-shell") {
		args = append(args, "--headless=new")
	}
	if resolverRule != "" {
		args = append(args, "--host-resolver-rules="+resolverRule)
	}
	args = append(args, "about:blank")
	toBrowser, fromUs, err := os.Pipe()
	if err != nil {
		os.RemoveAll(dataDir)
		return nil, err
	}
	fromBrowser, toUs, err := os.Pipe()
	if err != nil {
		toBrowser.Close()
		fromUs.Close()
		os.RemoveAll(dataDir)
		return nil, err
	}
	cmd := exec.Command(path, args...)
	cmd.ExtraFiles = []*os.File{toBrowser, toUs} // fd 3: commands in, fd 4: replies out
	stderr := &tail{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		for _, f := range []*os.File{toBrowser, fromUs, fromBrowser, toUs} {
			f.Close()
		}
		os.RemoveAll(dataDir)
		return nil, fmt.Errorf("start browser: %w", err)
	}
	toBrowser.Close()
	toUs.Close()
	return &browser{cdp: newCDP(fromUs, fromBrowser), cmd: cmd, dataDir: dataDir, stderr: stderr}, nil
}

func (b *browser) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = b.call(ctx, "", "Browser.close", nil, nil)
	cancel()
	done := make(chan struct{})
	go func() { _ = b.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = b.cmd.Process.Kill()
		<-done
	}
	if w, ok := b.w.(*os.File); ok {
		w.Close()
	}
	os.RemoveAll(b.dataDir)
}
