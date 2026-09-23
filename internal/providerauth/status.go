package providerauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codex-cliproxy-gateway/internal/config"
)

type Status struct {
	Provider   config.ProviderSpec
	Configured bool
}

type Checker struct {
	Client *http.Client
}

func (c Checker) Check(ctx context.Context, cfg config.Config, providerID, requestedModel string) (Status, error) {
	provider, ok := cfg.Provider(providerID)
	if !ok {
		return Status{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	key, err := cfg.ResolveCLIProxyAPIKey()
	if err != nil {
		return Status{Provider: provider}, fmt.Errorf("resolve CLIProxyAPI access key: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.CLIProxyBaseURL, "/")+"/models", nil)
	if err != nil {
		return Status{Provider: provider}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.client().Do(req)
	if err != nil {
		return Status{Provider: provider}, fmt.Errorf("query CLIProxyAPI models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return Status{Provider: provider}, fmt.Errorf("CLIProxyAPI models endpoint returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return Status{Provider: provider}, fmt.Errorf("parse CLIProxyAPI models: %w", err)
	}
	models := make(map[string]struct{}, len(envelope.Data))
	for _, item := range envelope.Data {
		models[item.ID] = struct{}{}
	}
	_, hasIdentityModel := models[provider.RequiredModel]
	hasRequestedModel := true
	if requestedModel != "" {
		_, hasRequestedModel = models[requestedModel]
	}
	return Status{Provider: provider, Configured: hasIdentityModel && hasRequestedModel}, nil
}

func (c Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 3 * time.Second}
}

func LoginMessage(provider config.ProviderSpec, configPath string, rejected bool) string {
	state := "is not configured"
	if rejected {
		state = "rejected the configured credentials"
	}
	return fmt.Sprintf("%s %s. Run 'codex-cliproxy-gateway login %s' or visit %s. Add or replace the API key in %s, then retry.", provider.DisplayName, state, provider.ID, provider.SetupURL, configPath)
}

func ErrorCode(providerID, suffix string) string {
	providerID = strings.ReplaceAll(strings.ToLower(providerID), "-", "_")
	return providerID + "_" + suffix
}
