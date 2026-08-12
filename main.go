// Command kb serves the embedded kanban SPA and its API on top of the
// SQLite store. All HTTP behavior — auth modes (Entra ID bearer tokens,
// shared-secret token, open; see internal/server), board/labels/settings/AI
// endpoints, SPA serving — lives in internal/server; storage lives in
// internal/store. Subcommands (kb mcp) dispatch in dispatch.go.
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

//go:embed all:dist
var distFS embed.FS

// minSecretBytes is the shortest settings-encryption secret that is accepted;
// the generated one is 32 random bytes. It tracks the threshold the shared
// secret path warns at, so serve and the other entry points draw the line in
// the same place — serve refuses, they warn and continue.
const minSecretBytes = store.EnvSecretMinBytes

// HTTP timeouts. A request that stalls without these pins a connection
// forever, so an idle browser tab or a half-open socket is a free denial of
// service. writeTimeout is the loose one on purpose: it has to cover the AI
// proxy's upstream round trip *and* leave budget to write the answer. Go arms
// the write deadline when the request headers are read, not when the handler
// starts writing, so a budget merely equal to server.AITimeout is spent
// entirely upstream and the 502 the proxy produces on a timeout — its most
// common failure — dies as a killed connection instead of reaching the client.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = server.AITimeout + 30*time.Second
	idleTimeout       = 120 * time.Second
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type emptyCloser struct{}

func (emptyCloser) Close() error { return nil }

// logSafe keeps operator-controlled paths and errors on one physical log line.
func logSafe(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// configureLogging points the standard logger at path, returning the closer the
// caller defers. An empty path leaves stderr alone.
func configureLogging(path string) (io.Closer, error) {
	if path == "" {
		return emptyCloser{}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "kb: writing logs to %q\n", logSafe(path))
	log.SetOutput(file)
	return file, nil
}

// checkSecret rejects a truncated or hand-made settings-encryption secret. An
// empty <data>/secret derives the AES key from SHA-256("") — a key anyone can
// compute — and the store would use it without complaint. Refusing to start
// is the only safe answer: regenerating silently would orphan whatever is
// already encrypted under the old secret.
func checkSecret(secret []byte, dataDir string) error {
	if len(secret) >= minSecretBytes {
		return nil
	}
	return fmt.Errorf("secret is %d bytes, need at least %d: set KB_SECRET, or delete %s to have a new one generated (any stored AI keys become unreadable)",
		len(secret), minSecretBytes, filepath.Join(dataDir, "secret"))
}

// newHTTPServer applies the timeouts a bare http.ListenAndServe leaves off.
// Without them a stalled or half-open connection is held forever, so an idle
// tab or a slowloris client is a free denial of service.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

var listenHTTPServer = func(srv *http.Server) error {
	return srv.ListenAndServe()
}

var (
	loadOrCreateSecret = store.LoadOrCreateSecret
	openDataStore      = store.Open
	importMarkdownDir  = func(st *store.Store, dir string) (int, error) { return st.ImportMarkdownDir(dir) }
	subDistFS          = fs.Sub
	runMainServer      = runWebServer
	fatalLog           = log.Fatal
)

type webFlagError struct{ err error }

func (e *webFlagError) Error() string { return e.err.Error() }
func (e *webFlagError) Unwrap() error { return e.err }

// rootUsageText is what `kb --help` prints before the serve flags. Every
// registered subcommand must appear here; TestRootUsageNamesEverySubcommand
// fails when the dispatch table and this text drift apart.
const rootUsageText = `usage: kb [flags]            serve the web UI (default)
       kb <command> [args]   work with tasks from the terminal

commands:
  add, list, update, move, done, cancel, restore, rm
             the task CLI — run "kb help" for the full reference
  mcp        serve the board to AI agents over MCP stdio
  help       task CLI reference

serve flags:
`

// runWebServer performs one web-server startup and returns startup/runtime
// failures to main. Keeping process termination at the outermost boundary
// makes every wiring branch testable without forking a subprocess.
func runWebServer(args []string) error {
	return runWebServerWithFlagOutput(args, os.Stderr)
}

func runWebServerWithFlagOutput(args []string, output io.Writer) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	port := flags.String("port", envOr("KB_PORT", "8080"), "listen port (env KB_PORT)")
	dataDir := flags.String("data", defaultDataDir(), "board storage directory (env KB_DATA)")
	logPath := flags.String("log", envOr("KB_LOG_FILE", ""), "write logs to this file instead of stderr (env KB_LOG_FILE)")
	// The default FlagSet usage only lists the serve flags, which hid the
	// entire subcommand surface from `kb --help`; the task CLI was only
	// discoverable by already knowing to type `kb help`.
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), rootUsageText)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return &webFlagError{err: err}
	}

	logCloser, err := configureLogging(*logPath)
	if err != nil {
		return fmt.Errorf("configure log file %q: %s", logSafe(*logPath), logSafe(err.Error()))
	}
	defer logCloser.Close()

	cfg := server.Config{
		Token:        os.Getenv("KB_TOKEN"),
		TenantID:     os.Getenv("KB_AZURE_TENANT_ID"),
		ClientID:     os.Getenv("KB_AZURE_CLIENT_ID"),
		AllowedHosts: os.Getenv("KB_ALLOWED_HOSTS"),
	}
	if (cfg.TenantID != "") != (cfg.ClientID != "") {
		return fmt.Errorf("KB_AZURE_TENANT_ID and KB_AZURE_CLIENT_ID must be set together")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	secret, err := loadOrCreateSecret(*dataDir)
	if err != nil {
		return fmt.Errorf("load secret: %w", err)
	}
	if err := checkSecret(secret, *dataDir); err != nil {
		return err
	}
	st, err := openDataStore(filepath.Join(*dataDir, "kb.db"), secret)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	imported, err := importMarkdownDir(st, *dataDir)
	if err != nil {
		return fmt.Errorf("import markdown boards: %w", err)
	}
	log.Printf("imported %d markdown board(s)", imported)
	static, err := subDistFS(distFS, "dist")
	if err != nil {
		return fmt.Errorf("embedded dist: %w", err)
	}

	mode := "open"
	switch {
	case cfg.TenantID != "":
		mode = "entra"
	case cfg.Token != "":
		mode = "token"
	}
	// Open mode has no authentication and the store can hold encrypted AI
	// API keys, so by default it must never listen beyond the local machine.
	// KB_BIND overrides the bind address explicitly (any mode).
	host := os.Getenv("KB_BIND")
	if host == "" && mode == "open" {
		host = "127.0.0.1"
	}
	addr := host + ":" + *port
	log.Printf("kb listening on %s (auth: %s, data: %s)", addr, mode, *dataDir)
	if mode == "open" && cfg.AllowedHosts == "" && host != "127.0.0.1" && host != "localhost" {
		log.Printf("warning: open mode accepts only loopback Host headers; set KB_ALLOWED_HOSTS to serve the API on another hostname")
	}
	return listenHTTPServer(newHTTPServer(addr, server.New(cfg, static, st)))
}

func main() {
	if dispatch() {
		return
	}
	if err := runMainServer(os.Args[1:]); err != nil {
		var flagErr *webFlagError
		if errors.As(err, &flagErr) {
			if errors.Is(flagErr, flag.ErrHelp) {
				return
			}
			exitProcess(2)
			return
		}
		fatalLog(err)
	}
}
