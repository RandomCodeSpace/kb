package server

import (
	"net/http"
	"os"
	"time"
)

const forgeTimeout = 20 * time.Second

func newForgeClient() *http.Client {
	raw := os.Getenv("KB_FORGE_ALLOW_PRIVATE")
	allowAll := raw == "1" || raw == "*"
	var allowHosts map[string]bool
	if !allowAll {
		allowHosts = parseAllowedHosts(raw)
	}

	return &http.Client{
		Timeout:       forgeTimeout,
		Transport:     guardedTransport(allowHosts, allowAll),
		CheckRedirect: sameHostRedirect,
	}
}
