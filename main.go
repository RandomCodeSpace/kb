// Command kb serves the embedded kanban SPA and its API on top of the
// SQLite store. All HTTP behavior — auth modes (Entra ID bearer tokens,
// shared-secret token, open; see internal/server), board/labels/settings/AI
// endpoints, SPA serving — lives in internal/server; storage lives in
// internal/store. Subcommands (kb mcp) dispatch in dispatch.go.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

//go:embed all:dist
var distFS embed.FS

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if dispatch() {
		return
	}
	port := flag.String("port", envOr("KB_PORT", "8080"), "listen port (env KB_PORT)")
	dataDir := flag.String("data", defaultDataDir(), "board storage directory (env KB_DATA)")
	flag.Parse()

	cfg := server.Config{
		Token:    os.Getenv("KB_TOKEN"),
		TenantID: os.Getenv("KB_AZURE_TENANT_ID"),
		ClientID: os.Getenv("KB_AZURE_CLIENT_ID"),
	}
	if (cfg.TenantID != "") != (cfg.ClientID != "") {
		log.Fatal("KB_AZURE_TENANT_ID and KB_AZURE_CLIENT_ID must be set together")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	secret, err := store.LoadOrCreateSecret(*dataDir)
	if err != nil {
		log.Fatalf("load secret: %v", err)
	}
	st, err := store.Open(filepath.Join(*dataDir, "kb.db"), secret)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()
	imported, err := st.ImportMarkdownDir(*dataDir)
	if err != nil {
		log.Fatalf("import markdown boards: %v", err)
	}
	log.Printf("imported %d markdown board(s)", imported)
	static, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("embedded dist: %v", err)
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
	if err := http.ListenAndServe(addr, server.New(cfg, static, st)); err != nil {
		log.Fatal(err)
	}
}
