package devstation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCaddyIntegration(t *testing.T) {
	if os.Getenv("DEVSTATION_INTEGRATION") != "1" {
		t.Skip("set DEVSTATION_INTEGRATION=1 with caddy on PATH")
	}
	c := testConfig(t)
	configDir := c.dir
	c.dir = filepath.Join(configDir, "state")
	if err := os.Mkdir(c.dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.Listen = listener.Addr().String()
	listener.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*.dev.example.com"}, DNSNames: []string{"*.dev.example.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c.Certificate = filepath.Join(c.dir, "cert.pem")
	c.Key = filepath.Join(c.dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = os.WriteFile(c.Certificate, certPEM, 0600)
	_ = os.WriteFile(c.Key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, rw, e := w.(http.Hijacker).Hijack()
			if e != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\nhello-upgrade")
			_ = rw.Flush()
			return
		}
		fmt.Fprint(w, "backend-one")
	}))
	defer backend.Close()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	port, _ := strconv.Atoi(p)
	if err = apply(c, []Route{{Name: "first", Port: port}}, true, runCaddy); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	cmd := exec.Command("caddy", "run", "--config", c.statePath())
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	}()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, c.Listen)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, e := client.Get(c.url("first"))
		if e == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(50 * time.Millisecond)
	}
	check := func(name string, status int, body string) {
		t.Helper()
		resp, e := client.Get(c.url(name))
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status || (body != "" && string(b) != body) {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, b)
		}
	}
	check("first", 200, "backend-one")
	check("unknown", 404, "")
	// Caddy must preserve an HTTP upgrade and the resulting byte stream.
	conn, err := tls.Dial("tcp", c.Listen, &tls.Config{RootCAs: roots, ServerName: "first.dev.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: first.dev.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 101 {
		t.Fatal(resp.Status)
	}
	upgraded := make([]byte, len("hello-upgrade"))
	_, err = io.ReadFull(reader, upgraded)
	conn.Close()
	if err != nil || string(upgraded) != "hello-upgrade" {
		t.Fatalf("upgrade: %q %v", upgraded, err)
	}
	if err = apply(c, []Route{{Name: "second", Port: port}}, false, runCaddy); err != nil {
		t.Fatal(err)
	}
	check("first", 404, "")
	check("second", 200, "backend-one")
	staticDir := filepath.Join(c.dir, "site")
	if err := os.Mkdir(staticDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("site-index"), 0600); err != nil {
		t.Fatal(err)
	}
	staticFile := filepath.Join(c.dir, "single file?#%.html")
	if err := os.WriteFile(staticFile, []byte("single-file"), 0600); err != nil {
		t.Fatal(err)
	}
	withStatic := []Route{{Name: "second", Port: port}, {Name: "site", Kind: "directory", Path: staticDir}, {Name: "single", Kind: "file", Path: staticFile}}
	if err := apply(c, withStatic, false, runCaddy); err != nil {
		t.Fatal(err)
	}
	check("site", 200, "site-index")
	check("single", 200, "single-file")
	check("second", 200, "backend-one")
	configPath := filepath.Join(configDir, "config.toml")
	configData := fmt.Sprintf("domain = %q\nlisten = %q\ncertificate = %q\nkey = %q\n", c.Domain, c.Listen, c.Certificate, c.Key)
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"--config", configPath, "serve", staticFile, "--name", "single"}, "test", io.Discard); err != nil {
		t.Fatal(err)
	}
	check("single", 200, "single-file")
	parsed, err := readRoutes(c.statePath())
	if err != nil || len(parsed) != 3 {
		t.Fatalf("static routes not saved: %v %v", parsed, err)
	}
	// The first publish creates the artifacts route; later publishes only write files.
	t.Setenv("XDG_DATA_HOME", filepath.Join(configDir, "data"))
	shots := filepath.Join(configDir, "shots")
	if err := os.MkdirAll(shots, 0700); err != nil {
		t.Fatal(err)
	}
	image := []byte("\x89PNG fake image bytes")
	_ = os.WriteFile(filepath.Join(shots, "board-dark-1440.png"), image, 0600)
	publish := func() {
		t.Helper()
		if err := Run([]string{"--config", configPath, "publish", shots, "--project", "demo", "--name", "board"}, "test", io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	noRedirect := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, status int) (string, http.Header) {
		t.Helper()
		resp, e := noRedirect.Get(c.url("artifacts") + path)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s: %d %q", path, resp.StatusCode, b)
		}
		return string(b), resp.Header
	}
	page, header := get("/demo/board/", 200)
	if header.Get("Cache-Control") != "no-cache" || header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(page, "v/1/board-dark-1440.png") {
		t.Fatalf("artifact page: %q %q", header.Get("Cache-Control"), page)
	}
	if body, _ := get("/demo/board/v/1/board-dark-1440.png", 200); body != string(image) {
		t.Fatal("published image differs")
	}
	if _, h := get("/demo/board", 308); h.Get("Location") != "/demo/board/" {
		t.Fatalf("redirect: %q", h.Get("Location"))
	}
	if body, _ := get("/", 200); !strings.Contains(body, "./demo/") {
		t.Fatal("root index misses the project")
	}
	// Comments reach `dev daemon` through Caddy on the same origin.
	daemonCtx, stopDaemon := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveDaemon(daemonCtx, daemonHandler(c), daemonSocket(c.dir), io.Discard)
	}()
	defer func() { stopDaemon(); <-served }()
	post := func(path, body, origin string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", c.url("artifacts")+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		for deadline := time.Now().Add(5 * time.Second); ; {
			resp, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway || time.Now().After(deadline) {
				return resp.StatusCode
			}
			time.Sleep(50 * time.Millisecond) // the daemon is still starting
			req, _ = http.NewRequest("POST", c.url("artifacts")+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", origin)
		}
	}
	if code := post("/_devstation/comments/demo/board", `{"path":"board-dark-1440.png","version":1,"x":0.5,"y":0.5,"text":"clipped"}`, c.url("artifacts")); code != http.StatusCreated {
		t.Fatalf("comment through Caddy: %d", code)
	}
	if code := post("/_devstation/comments/demo/board", `{"path":"board-dark-1440.png","version":1,"text":"x"}`, "https://evil.example.com"); code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", code)
	}
	if code := post("/_devstation/other", `{}`, c.url("artifacts")); code != http.StatusNotFound {
		t.Fatalf("unknown daemon path: %d", code)
	}
	if body, _ := get("/demo/board/comments.jsonl", 200); !strings.Contains(body, "clipped") {
		t.Fatalf("comment log: %q", body)
	}
	// A mockup opens on its review page and takes comments on its elements.
	mock := filepath.Join(configDir, "mock")
	_ = os.MkdirAll(mock, 0700)
	_ = os.WriteFile(filepath.Join(mock, "index.html"), []byte("<button id=save>Save</button>"), 0600)
	if err := Run([]string{"--config", configPath, "publish", mock, "--project", "demo", "--name", "mock"}, "test", io.Discard); err != nil {
		t.Fatal(err)
	}
	if page, _ := get("/demo/mock/", 200); !strings.Contains(page, `"./review/1/"`) {
		t.Fatalf("mockup does not open its review page: %q", page)
	}
	if page, _ := get("/demo/mock/review/1/", 200); !strings.Contains(page, `<iframe id="mockup"`) {
		t.Fatalf("review page: %q", page)
	}
	if body, _ := get("/demo/mock/v/1/index.html", 200); body != "<button id=save>Save</button>" {
		t.Fatalf("mockup changed: %q", body)
	}
	if code := post("/_devstation/comments/demo/mock", `{"path":"index.html","version":1,"x":0.5,"y":0.5,"anchor":{"selector":"button#save","quote":"Save","width":1280,"height":800,"theme":"light"},"text":"bigger"}`, c.url("artifacts")); code != http.StatusCreated {
		t.Fatalf("mockup comment through Caddy: %d", code)
	}
	_ = os.WriteFile(filepath.Join(shots, "board-dark-1440.png"), append(image, '!'), 0600)
	publish()
	if page, _ = get("/demo/board/", 200); !strings.Contains(page, "v/2/board-dark-1440.png") {
		t.Fatal("stable page does not show version 2")
	}
	get("/demo/board/compare/1-2/", 200)
	check("second", 200, "backend-one")
	if err := Run([]string{"--config", configPath, "unpublish", "demo/board"}, "test", io.Discard); err != nil {
		t.Fatal(err)
	}
	get("/demo/board/", 404)
	parsed, err = readRoutes(c.statePath())
	if err != nil || len(parsed) != 4 {
		t.Fatalf("artifacts route not saved: %v %v", parsed, err)
	}
	// Invalid certificate must not break the running route or persisted config.
	saved, _ := os.ReadFile(c.statePath())
	bad := c
	bad.Certificate = "/does-not-exist"
	if err = apply(bad, nil, false, runCaddy); err == nil {
		t.Fatal("accepted invalid TLS")
	}
	after, _ := os.ReadFile(c.statePath())
	if !bytes.Equal(saved, after) {
		t.Fatal("changed saved configuration on validation failure")
	}
	check("second", 200, "backend-one")
	if err = apply(c, nil, false, runCaddy); err != nil {
		t.Fatal(err)
	}
	check("second", 404, "")
}
