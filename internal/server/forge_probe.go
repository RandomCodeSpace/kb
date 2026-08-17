package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// ForgeProber tests one stored forge integration without going through kb's
// HTTP API. It is shared by local frontends so they use the same guarded
// client, credential-origin rule, endpoint derivation, and response handling
// as the server handler.
type ForgeProber struct {
	store  *store.Store
	client *http.Client
}

// NewForgeProber constructs a direct-store forge connection prober.
func NewForgeProber(st *store.Store) *ForgeProber {
	return &ForgeProber{store: st, client: newForgeClient()}
}

// Probe tests unsaved baseURL and pat values over one stored integration.
// Blank supplied values retain the stored value. The stored token is never
// sent to a supplied origin that differs from the saved one.
func (p *ForgeProber) Probe(ctx context.Context, user, name, baseURL, pat string) error {
	kind, storedBase, storedPAT, err := p.store.ForgePAT(user, name)
	if err != nil {
		return errors.New("integration unavailable")
	}
	probe := forgeTestProbe{}
	if strings.TrimSpace(baseURL) != "" {
		probe.BaseURL = &baseURL
	}
	if strings.TrimSpace(pat) != "" {
		probe.PAT = &pat
	}
	target, err := resolveForgeTestTarget(storedBase, storedPAT, probe)
	if err != nil {
		return err
	}
	request, err := newForgeTestRequest(ctx, kind, target)
	if err != nil {
		return err
	}
	if err := executeForgeTest(p.client, request); err != nil {
		return errors.New(connectionFailedMessage)
	}
	return nil
}
