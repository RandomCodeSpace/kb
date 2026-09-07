// Package webui serves the kb board to a local browser: a JSON API under
// /api/ backed by the same store the CLI uses, plus an embedded static UI.
// The contract lives in docs/web-api.md.
package webui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// The all: prefix keeps the embed valid while the directory holds only a
// placeholder; the UI files themselves are ordinary names.
//
//go:embed all:static
var staticFS embed.FS

// defaultAddr is the loopback, random-port bind used when Options.Addr is
// empty.
const defaultAddr = "127.0.0.1:0"

// Options configures Run.
type Options struct {
	// DataDir is the kb data directory; empty means the CLI default.
	DataDir string
	// User is the board owner; empty means "default".
	User string
	// Version is the launching binary's display version, reported by
	// /api/meta.
	Version string
	// Addr is the listen address; empty means 127.0.0.1:0.
	Addr string
	// OpenBrowser launches the system browser on the served URL.
	OpenBrowser bool
	// AllowRemote permits a non-loopback Addr. Off by default because the API
	// has no authentication: anything that can reach the port owns the board.
	AllowRemote bool
}

// Run opens the local store, listens on opts.Addr, prints the served URL to
// stdout and serves until SIGINT or SIGTERM.
func Run(opts Options, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, opts, stdout, stderr)
}

// run is Run without the signal wiring: cancelling ctx shuts the server down.
func run(ctx context.Context, opts Options, stdout, stderr io.Writer) error {
	user := opts.User
	if user == "" {
		user = "default"
	}
	st, err := cliapp.OpenLocalStore(opts.DataDir, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	ln, err := Listen(opts.Addr, opts.AllowRemote)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "kb web: serving %s\n", url)
	if opts.OpenBrowser {
		if err := launchBrowser(url); err != nil {
			fmt.Fprintf(stderr, "kb web: open browser: %v\n", err)
		}
	}
	srv := &http.Server{Handler: NewHandler(st, user, opts.DataDir, opts.Version), ReadHeaderTimeout: 10 * time.Second}
	return serve(ctx, srv, ln)
}

// serve blocks on srv until ctx is cancelled (a clean stop) or the listener
// fails.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	})
	defer stop()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Listen binds addr (empty means 127.0.0.1:0). Unless allowRemote is set, a
// host that is not loopback is refused before any socket is opened.
func Listen(addr string, allowRemote bool) (net.Listener, error) {
	if addr == "" {
		addr = defaultAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("kb web: invalid address %q: %w", addr, err)
	}
	if !allowRemote && !isLoopbackHost(host) {
		return nil, fmt.Errorf("kb web: refusing to listen on non-loopback address %q (pass --unsafe-listen to allow it)", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("kb web: listen: %w", err)
	}
	return ln, nil
}

// isLoopbackHost accepts "localhost" and literal loopback IPs. An empty host
// means every interface, which is exactly the case to refuse.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// launchBrowser opens url in the system browser. A variable so tests can
// stub it.
var launchBrowser = func(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	return exec.Command(name, args...).Start()
}

// browserCommand picks the platform's URL opener.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
	return "xdg-open", []string{url}
}

// staticHandler serves the embedded UI. Unknown paths fall back to
// index.html so a bookmarked client-side route still loads the app.
type staticHandler struct {
	files fs.FS
	index []byte
}

func newStaticHandler() *staticHandler {
	files, _ := fs.Sub(staticFS, "static")
	index, _ := fs.ReadFile(files, "index.html")
	return &staticHandler{files: files, index: index}
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if info, err := fs.Stat(h.files, name); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, h.files, name)
			return
		}
	}
	if h.index == nil {
		http.Error(w, "index.html is not embedded", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.index)
}

// NewHandler is the testable core: JSON API under /api/ and the embedded
// static UI at /.
func NewHandler(st *store.Store, user, dataDir, version string) http.Handler {
	s := &server{st: st, user: user, dataDir: dataDir, version: version}
	mux := http.NewServeMux()
	s.routes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", newStaticHandler())
	return secure(mux)
}
