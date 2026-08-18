package forge

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// ForgeProber tests stored or draft forge integration values without going
// through kb's HTTP API. It is shared by local frontends so they use the same guarded
// client, credential-origin rule, endpoint derivation, and response handling
// as the server handler.
type ForgeProber struct {
	store  *store.Store
	client *http.Client
}

// ForgeProbeConfig is one connection test candidate. Saved selects whether
// blank endpoint credentials may fall back to the named stored integration;
// drafts set it false and are tested entirely from these unsaved values.
// Project is probe-only and is never persisted with the integration.
type ForgeProbeConfig struct {
	Name    string
	Kind    string
	BaseURL string
	Project string
	Token   string
	Saved   bool
}

// NewForgeProber constructs a direct-store forge connection prober.
func NewForgeProber(st *store.Store) *ForgeProber {
	return &ForgeProber{store: st, client: NewHTTPClient()}
}

func NewForgeProberWithClient(st *store.Store, client *http.Client) *ForgeProber {
	if client == nil {
		client = NewHTTPClient()
	}
	return &ForgeProber{store: st, client: client}
}

// Probe tests one unsaved candidate. Persisted rows may retain blank stored
// endpoint values; drafts never touch the store. A stored token is never sent
// to a supplied origin that differs from the saved one.
func (p *ForgeProber) Probe(ctx context.Context, user string, config ForgeProbeConfig) error {
	if strings.TrimSpace(config.Name) == "" {
		return errors.New("integration name is required")
	}
	kind := strings.TrimSpace(config.Kind)
	storedBase, storedToken := "", ""
	if config.Saved {
		storedKind, baseURL, token, err := p.store.ForgePAT(user, config.Name)
		if err != nil {
			return errors.New("integration unavailable")
		}
		if kind == "" {
			kind = storedKind
		}
		storedBase, storedToken = baseURL, token
	}
	probe := forgeTestProbe{}
	if strings.TrimSpace(config.BaseURL) != "" {
		probe.BaseURL = &config.BaseURL
	}
	if strings.TrimSpace(config.Token) != "" {
		probe.PAT = &config.Token
	}
	target, err := resolveForgeTestTarget(storedBase, storedToken, probe)
	if err != nil {
		return err
	}
	request, err := newForgeTestRequest(ctx, kind, target, strings.TrimSpace(config.Project))
	if err != nil {
		return err
	}
	if err := executeForgeTest(p.client, request); err != nil {
		return errors.New(connectionFailedMessage)
	}
	return nil
}
