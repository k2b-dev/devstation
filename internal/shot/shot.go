// Package shot takes screenshots of web pages in several color schemes and
// widths with a local headless Chromium. File names follow the artifact
// gallery grammar: <label>-<theme>-<width>.png.
package shot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Target struct{ Label, URL string }

type Cookie struct{ Name, Value string }

type Options struct {
	Targets     []Target
	Out         string
	Themes      []string // light, dark, hell, dunkel
	Widths      []int
	Height      int // 0: 900, or 844 below 600 px width
	FullPage    bool
	Scale       float64 // 0: 1
	Cookies     []Cookie
	ThemeCookie string // cookie set to "light" or "dark" per theme
	Eval        string // JavaScript run after the page loaded
	Clicks      []string
	Hover       string
	WaitFor     string
	Browser     string
	Resolver    string // Chromium --host-resolver-rules
}

type Shot struct {
	File     string `json:"file"`
	URL      string `json:"url"`
	FinalURL string `json:"final_url"`
	Status   int    `json:"status"`
	Theme    string `json:"theme"`
	Width    int    `json:"width"`
}

type Result struct {
	Out      string   `json:"out"`
	Files    []Shot   `json:"files"`
	Warnings []string `json:"warnings"`
}

// Waits, as variables for tests.
var (
	loadWait     = 30 * time.Second       // for the load event
	settleWait   = 5 * time.Second        // after load, for requests to finish
	quietPeriod  = 500 * time.Millisecond // without requests in flight
	selectorWait = 10 * time.Second       // for each --click, --hover and --wait-for
)

var (
	labelChars  = regexp.MustCompile(`[^a-z0-9]+`)
	schemes     = map[string]string{"light": "light", "hell": "light", "dark": "dark", "dunkel": "dark"}
	stabilizeJS = `(() => {
  const s = document.createElement("style");
  s.textContent = "*,*::before,*::after{animation-duration:0s!important;animation-delay:0s!important;transition:none!important;caret-color:transparent!important}";
  document.documentElement.appendChild(s);
})()`
	settleJS = `(async () => {
  await document.fonts.ready;
  await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
})()`
)

// ParseTarget reads LABEL=URL or URL. Without a label, it comes from the path.
func ParseTarget(arg string) (Target, error) {
	label, raw := "", arg
	if i := strings.Index(arg, "="); i > 0 && !strings.Contains(arg[:i], "/") && !strings.Contains(arg[:i], ":") {
		label, raw = arg[:i], arg[i+1:]
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Target{}, fmt.Errorf("%q is not an http(s) URL", raw)
	}
	if u.User != nil {
		return Target{}, fmt.Errorf("%q contains credentials; pass them as --cookie NAME=@FILE", u.Redacted())
	}
	if label == "" {
		label = strings.ReplaceAll(strings.Trim(u.Path, "/"), "/", "-")
		if label == "" {
			label = strings.Split(u.Hostname(), ".")[0]
		}
	}
	label = strings.Trim(labelChars.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if label == "" {
		return Target{}, fmt.Errorf("no usable label for %q; pass LABEL=URL", arg)
	}
	return Target{Label: label, URL: u.String()}, nil
}

func (o Options) validate() error {
	if len(o.Targets) == 0 || o.Out == "" {
		return errors.New("pass at least one URL and --out")
	}
	labels := map[string]string{}
	for _, t := range o.Targets {
		if other, ok := labels[t.Label]; ok {
			return fmt.Errorf("%s and %s would both be saved as %q; name them with LABEL=URL", other, t.URL, t.Label)
		}
		labels[t.Label] = t.URL
	}
	if len(o.Themes) == 0 || len(o.Widths) == 0 {
		return errors.New("pass at least one theme and one width")
	}
	for _, t := range o.Themes {
		if schemes[t] == "" {
			return fmt.Errorf("unknown theme %q; use light, dark, hell or dunkel", t)
		}
	}
	for _, w := range o.Widths {
		if w < 200 || w > 7680 {
			return fmt.Errorf("width %d is out of range (200–7680)", w)
		}
	}
	if o.Scale < 0 || o.Scale > 4 {
		return errors.New("--scale must be between 0 and 4")
	}
	return nil
}

// Run takes one screenshot per target, theme and width, in that order. On an
// error it returns the shots taken so far.
func Run(ctx context.Context, o Options) (Result, error) {
	r := Result{Out: o.Out, Files: []Shot{}, Warnings: []string{}}
	if err := o.validate(); err != nil {
		return r, err
	}
	if o.Scale == 0 {
		o.Scale = 1
	}
	path, err := FindBrowser(o.Browser)
	if err != nil {
		return r, err
	}
	if err = os.MkdirAll(o.Out, 0700); err != nil {
		return r, err
	}
	b, err := launch(path, o.Resolver)
	if err != nil {
		return r, err
	}
	defer b.close()
	if err = b.call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		return r, fmt.Errorf("browser did not start (%s): %w%s", path, err, b.stderrTail())
	}
	for _, t := range o.Targets {
		for _, theme := range o.Themes {
			for _, width := range o.Widths {
				s, warnings, err := b.take(ctx, o, t, theme, width)
				r.Warnings = append(r.Warnings, warnings...)
				if err != nil {
					return r, fmt.Errorf("%s (%s, %d px): %w", t.URL, theme, width, err)
				}
				r.Files = append(r.Files, s)
			}
		}
	}
	r.Warnings = append(r.Warnings, sameInBothSchemes(r.Files)...)
	return r, nil
}

// sameInBothSchemes warns when light and dark came out byte-identical, which
// usually means the page ignores prefers-color-scheme.
func sameInBothSchemes(files []Shot) []string {
	type key struct {
		url   string
		width int
	}
	byScheme := map[key]map[string]string{}
	for _, s := range files {
		k := key{s.URL, s.Width}
		if byScheme[k] == nil {
			byScheme[k] = map[string]string{}
		}
		byScheme[k][schemes[s.Theme]] = s.File
	}
	var warnings []string
	for _, files := range byScheme {
		light, dark := files["light"], files["dark"]
		if light == "" || dark == "" {
			continue
		}
		a, err1 := os.ReadFile(light)
		b, err2 := os.ReadFile(dark)
		if err1 == nil && err2 == nil && bytes.Equal(a, b) {
			warnings = append(warnings, fmt.Sprintf("%s and %s are identical; if the app reads its theme from a cookie, add --theme-cookie NAME", filepath.Base(light), filepath.Base(dark)))
		}
	}
	return warnings
}

func (b *browser) take(ctx context.Context, o Options, t Target, theme string, width int) (s Shot, warnings []string, err error) {
	s = Shot{URL: t.URL, Theme: theme, Width: width, File: filepath.Join(o.Out, fmt.Sprintf("%s-%s-%d.png", t.Label, theme, width))}
	warn := func(format string, a ...any) {
		warnings = append(warnings, fmt.Sprintf("%s-%s-%d: ", t.Label, theme, width)+fmt.Sprintf(format, a...))
	}
	// A fresh browser context per shot: storage, cookies and cache never
	// carry over from one theme or width to the next.
	var bc struct {
		ID string `json:"browserContextId"`
	}
	if err = b.call(ctx, "", "Target.createBrowserContext", map[string]any{}, &bc); err != nil {
		return s, nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = b.call(cleanup, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": bc.ID}, nil)
		cancel()
	}()
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err = b.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": bc.ID}, &target); err != nil {
		return s, nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err = b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return s, nil, err
	}
	sid := attached.SessionID
	call := func(method string, params, out any) error { return b.call(ctx, sid, method, params, out) }
	events := b.subscribe(sid, "Page.frameNavigated", "Page.lifecycleEvent", "Network.requestWillBeSent", "Network.loadingFinished",
		"Network.loadingFailed", "Network.responseReceived", "Runtime.exceptionThrown", "Inspector.targetCrashed")
	defer b.unsubscribe(events)
	// Dismiss alert, confirm and prompt dialogs; they would block the page.
	// The handler runs beside take, which may be blocked in a click meanwhile;
	// its notes join the warnings once it stopped.
	dialogs := b.subscribe(sid, "Page.javascriptDialogOpening")
	done := make(chan struct{})
	var wg sync.WaitGroup
	var dialogNotes []string
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case m := <-dialogs.ch:
				var p struct{ Type, Message string }
				_ = json.Unmarshal(m.Params, &p)
				dialogNotes = append(dialogNotes, fmt.Sprintf("%s-%s-%d: dismissed the %s dialog %q", t.Label, theme, width, p.Type, p.Message))
				_ = b.call(ctx, sid, "Page.handleJavaScriptDialog", map[string]any{"accept": false}, nil)
			case <-done:
				return
			}
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		b.unsubscribe(dialogs)
		warnings = append(warnings, dialogNotes...)
	}()
	pageErrors := 0
	pageError := func(m message) {
		if pageErrors++; pageErrors <= 3 {
			warn("page error: %s", exceptionText(m.Params))
		} else if pageErrors == 4 {
			warn("more page errors omitted")
		}
	}

	height := o.Height
	if height == 0 {
		height = 900
		if width < 600 {
			height = 844
		}
	}
	phone := width < 600
	type step struct {
		method string
		params any
	}
	steps := []step{
		{"Page.enable", nil},
		{"Network.enable", nil},
		{"Runtime.enable", nil},
		{"Inspector.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": o.Scale, "mobile": phone}},
		{"Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": schemes[theme]}, {"name": "prefers-reduced-motion", "value": "reduce"}}}},
	}
	if phone {
		steps = append(steps, step{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}})
	}
	for _, st := range steps {
		if err = call(st.method, st.params, nil); err != nil {
			return s, warnings, err
		}
	}
	for _, c := range o.Cookies {
		if err = call("Network.setCookie", map[string]any{"name": c.Name, "value": c.Value, "url": t.URL, "path": "/", "httpOnly": true}, nil); err != nil {
			return s, warnings, err
		}
	}
	if o.ThemeCookie != "" {
		if err = call("Network.setCookie", map[string]any{"name": o.ThemeCookie, "value": schemes[theme], "url": t.URL, "path": "/"}, nil); err != nil {
			return s, warnings, err
		}
	}
	var nav struct {
		FrameID   string `json:"frameId"`
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err = call("Page.navigate", map[string]any{"url": t.URL}, &nav); err != nil {
		return s, warnings, err
	}
	if nav.ErrorText != "" {
		return s, warnings, fmt.Errorf("navigation failed: %s", nav.ErrorText)
	}
	if err = b.load(ctx, events, nav.FrameID, nav.LoaderID, &s, warn, pageError); err != nil {
		return s, warnings, err
	}
	if s.Status >= 400 {
		return s, warnings, fmt.Errorf("HTTP %d from %s", s.Status, s.FinalURL)
	}
	// Client-side redirects, such as to a sign-in page, show in the address.
	var shown string
	if err = b.eval(ctx, sid, "location.href", &shown); err != nil {
		return s, warnings, err
	}
	if shown != "" {
		s.FinalURL = shown
	}
	if !samePath(s.FinalURL, t.URL) {
		warn("shows %s instead; check that the session is still valid", s.FinalURL)
	}
	if err = b.eval(ctx, sid, stabilizeJS, nil); err != nil {
		return s, warnings, err
	}
	if o.Eval != "" {
		if err = b.eval(ctx, sid, o.Eval, nil); err != nil {
			return s, warnings, fmt.Errorf("--eval: %w", err)
		}
	}
	for _, sel := range o.Clicks {
		if err = b.pointer(ctx, sid, sel, true); err != nil {
			return s, warnings, err
		}
	}
	if o.Hover != "" {
		if err = b.pointer(ctx, sid, o.Hover, false); err != nil {
			return s, warnings, err
		}
	}
	if o.WaitFor != "" {
		if _, err = b.waitFor(ctx, sid, o.WaitFor, false); err != nil {
			return s, warnings, err
		}
	}
	if err = b.eval(ctx, sid, settleJS, nil); err != nil {
		return s, warnings, err
	}
	for drained := false; !drained; {
		select {
		case m := <-events.ch:
			switch m.Method {
			case "Runtime.exceptionThrown":
				pageError(m)
			case "Inspector.targetCrashed":
				return s, warnings, errors.New("page crashed")
			}
		default:
			drained = true
		}
	}
	params := map[string]any{"format": "png"}
	if o.FullPage {
		var metrics struct {
			CSSContentSize struct {
				Width, Height float64
			} `json:"cssContentSize"`
		}
		if err = call("Page.getLayoutMetrics", nil, &metrics); err != nil {
			return s, warnings, err
		}
		params["captureBeyondViewport"] = true
		params["clip"] = map[string]any{"x": 0, "y": 0, "width": width, "height": max(float64(height), metrics.CSSContentSize.Height), "scale": 1}
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err = call("Page.captureScreenshot", params, &shot); err != nil {
		return s, warnings, err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return s, warnings, err
	}
	return s, warnings, writeAtomic(s.File, png)
}

// load follows the main frame, including redirects by script, until its load
// event and then until no request (other than live streams) has been in
// flight for quietPeriod, at most settleWait.
func (b *browser) load(ctx context.Context, events *subscription, frameID, loaderID string, s *Shot, warn func(string, ...any), pageError func(message)) error {
	start := time.Now()
	loaded := false
	var loadedAt, quietSince time.Time
	type request struct{ url, loaderID string }
	inflight := map[string]request{}
	for {
		var deadline time.Time
		switch {
		case !loaded:
			deadline = start.Add(loadWait)
		case len(inflight) == 0:
			deadline = quietSince.Add(quietPeriod)
		default:
			deadline = loadedAt.Add(settleWait)
		}
		if limit := loadedAt.Add(settleWait); loaded && limit.Before(deadline) {
			deadline = limit
		}
		wait, cancel := context.WithDeadline(ctx, deadline)
		m, err := b.wait(wait, events, func(message) bool { return true })
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("page did not load in time: %w", ctx.Err())
			}
			switch {
			case !loaded && s.Status == 0:
				return fmt.Errorf("page did not load within %s", loadWait)
			case !loaded:
				warn("page still loading after %s; captured it as it was", loadWait)
			case len(inflight) > 0:
				for _, r := range inflight {
					warn("still loading after %s: %s; use --wait-for for content that comes later", settleWait, r.url)
					break
				}
			}
			return nil
		}
		switch m.Method {
		case "Page.frameNavigated":
			var p struct {
				Frame struct {
					ID       string `json:"id"`
					ParentID string `json:"parentId"`
					LoaderID string `json:"loaderId"`
				} `json:"frame"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.Frame.ID == frameID && p.Frame.ParentID == "" && p.Frame.LoaderID != loaderID {
				loaderID, loaded = p.Frame.LoaderID, false // a redirect by script or meta refresh
				// Requests of the replaced document do not always report an end.
				for id, r := range inflight {
					if r.loaderID != loaderID {
						delete(inflight, id)
					}
				}
			}
		case "Network.responseReceived":
			var p struct {
				Type     string `json:"type"`
				LoaderID string `json:"loaderId"`
				Response struct {
					URL    string `json:"url"`
					Status int    `json:"status"`
				} `json:"response"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.Type == "Document" && p.LoaderID == loaderID {
				s.Status, s.FinalURL = p.Response.Status, p.Response.URL
			}
		case "Page.lifecycleEvent":
			var p struct {
				LoaderID string `json:"loaderId"`
				Name     string `json:"name"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.LoaderID == loaderID && p.Name == "load" && !loaded {
				loaded, loadedAt, quietSince = true, time.Now(), time.Now()
			}
		case "Network.requestWillBeSent":
			var p struct {
				RequestID string `json:"requestId"`
				LoaderID  string `json:"loaderId"`
				Type      string `json:"type"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			// Live streams never finish; WebSockets do not appear here at all.
			if json.Unmarshal(m.Params, &p) == nil && p.Type != "EventSource" && !strings.HasPrefix(p.Request.URL, "data:") {
				inflight[p.RequestID] = request{p.Request.URL, p.LoaderID}
			}
		case "Network.loadingFinished", "Network.loadingFailed":
			var p struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				if _, ok := inflight[p.RequestID]; ok {
					delete(inflight, p.RequestID)
					if len(inflight) == 0 {
						quietSince = time.Now()
					}
				}
			}
		case "Runtime.exceptionThrown":
			pageError(m)
		case "Inspector.targetCrashed":
			return errors.New("page crashed")
		}
	}
}

// eval runs an expression, awaiting a returned promise, and decodes its value.
// replMode allows top-level await.
func (b *browser) eval(ctx context.Context, sid, expr string, out any) error {
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := b.call(ctx, sid, "Runtime.evaluate", map[string]any{"expression": expr, "awaitPromise": true, "returnByValue": true, "replMode": true}, &r); err != nil {
		return err
	}
	if len(r.ExceptionDetails) > 0 {
		return fmt.Errorf("script error: %s", exceptionText(json.RawMessage(`{"exceptionDetails":`+string(r.ExceptionDetails)+`}`)))
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

// waitFor polls until selector matches a visible element and returns its
// center. With scroll, it first scrolls the element into view if needed.
func (b *browser) waitFor(ctx context.Context, sid, selector string, scroll bool) ([2]float64, error) {
	quoted, _ := json.Marshal(selector)
	expr := `(() => { const e = document.querySelector(` + string(quoted) + `); if (!e) return null;
  let r = e.getBoundingClientRect();
  if (r.width === 0 || r.height === 0 || getComputedStyle(e).visibility === "hidden") return null;
  if (` + fmt.Sprint(scroll) + `) { e.scrollIntoView({block: "nearest", inline: "nearest"}); r = e.getBoundingClientRect(); }
  return [r.left + r.width / 2, r.top + r.height / 2]; })()`
	deadline := time.Now().Add(selectorWait)
	for {
		var point *[2]float64
		if err := b.eval(ctx, sid, expr, &point); err != nil {
			return [2]float64{}, err
		}
		if point != nil {
			return *point, nil
		}
		if time.Now().After(deadline) {
			return [2]float64{}, fmt.Errorf("no visible element matches %q after %s", selector, selectorWait)
		}
		select {
		case <-ctx.Done():
			return [2]float64{}, fmt.Errorf("waiting for %q: %w", selector, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// pointer moves the mouse over the element and clicks it if asked.
func (b *browser) pointer(ctx context.Context, sid, selector string, click bool) error {
	p, err := b.waitFor(ctx, sid, selector, true)
	if err != nil {
		return err
	}
	events := []string{"mouseMoved"}
	if click {
		events = append(events, "mousePressed", "mouseReleased")
	}
	for _, typ := range events {
		params := map[string]any{"type": typ, "x": p[0], "y": p[1]}
		if typ != "mouseMoved" {
			params["button"], params["clickCount"] = "left", 1
		}
		if err := b.call(ctx, sid, "Input.dispatchMouseEvent", params, nil); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(250 * time.Millisecond): // let the page react
	}
	return nil
}

func exceptionText(raw json.RawMessage) string {
	var p struct {
		ExceptionDetails struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(raw, &p)
	text := p.ExceptionDetails.Exception.Description
	if text == "" {
		text = p.ExceptionDetails.Text
	}
	if i := strings.IndexByte(text, '\n'); i > 0 {
		text = text[:i]
	}
	return text
}

func samePath(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return true
	}
	return ua.Host == ub.Host && strings.TrimSuffix(ua.Path, "/") == strings.TrimSuffix(ub.Path, "/")
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".shot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
