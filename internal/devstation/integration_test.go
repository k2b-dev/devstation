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
	if err = apply(c, []Route{{"first", port}}, true, runCaddy); err != nil {
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
	if err = apply(c, []Route{{"second", port}}, false, runCaddy); err != nil {
		t.Fatal(err)
	}
	check("first", 404, "")
	check("second", 200, "backend-one")
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
