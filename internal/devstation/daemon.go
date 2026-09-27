package devstation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/k2b-dev/devstation/internal/artifacts"
)

// runDaemon serves the dynamic parts of Devstation pages, which Caddy proxies
// from /_devstation/ on the artifacts route. Today that is the comment API.
func runDaemon(c Config, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: dev daemon")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveDaemon(ctx, daemonHandler(c), daemonSocket(c.dir), out)
}

func daemonHandler(c Config) http.Handler {
	mux := http.NewServeMux()
	// The route decides the store, as for every artifact command, so the
	// daemon follows it without a restart.
	comments := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store, _, _, err := artifactStore(c)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		artifacts.CommentHandler(store).ServeHTTP(w, r)
	})
	mux.Handle(artifacts.CommentsPrefix, http.StripPrefix(strings.TrimSuffix(artifacts.CommentsPrefix, "/"), comments))
	return mux
}

// serveDaemon listens on the Unix socket until ctx ends.
func serveDaemon(ctx context.Context, handler http.Handler, socket string, out io.Writer) error {
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode().Type() != fs.ModeSocket {
			return fmt.Errorf("%s exists and is not a socket", socket)
		}
		if conn, err := net.Dial("unix", socket); err == nil {
			conn.Close()
			return fmt.Errorf("another dev daemon is listening on %s", socket)
		}
		if err = os.Remove(socket); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		return err
	}
	// Closing the listener removes the socket, before requests in progress
	// finish; a daemon started meanwhile keeps its own socket.
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return err
	}
	// Caddy keeps idle upstream connections for two minutes; closing them
	// earlier races with its next request.
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 5 * time.Minute}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintln(out, "Listening on unix:"+socket)
	if err = server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped // requests in progress finish first
	return nil
}
