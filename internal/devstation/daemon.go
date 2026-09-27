package devstation

import (
	"context"
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
	store, _, _, err := artifactStore(c)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveDaemon(ctx, store, daemonSocket(c.dir), out)
}

func daemonHandler(store artifacts.Store) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(artifacts.CommentsPrefix, http.StripPrefix(strings.TrimSuffix(artifacts.CommentsPrefix, "/"), artifacts.CommentHandler(store)))
	return mux
}

// serveDaemon listens on the Unix socket until ctx ends.
func serveDaemon(ctx context.Context, store artifacts.Store, socket string, out io.Writer) error {
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
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{Handler: daemonHandler(store), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintln(out, "Listening on unix:"+socket)
	if err = server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
