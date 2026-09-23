package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codex-cliproxy-gateway/internal/catalog"
	"codex-cliproxy-gateway/internal/config"
)

type Result struct {
	Name string
	Err  error
}

type Runner struct {
	Client *http.Client
}

func (r Runner) Run(ctx context.Context, cfg config.Config, e2e bool, modelID string) []Result {
	results := []Result{{Name: "config", Err: cfg.Validate()}}
	_, catalogErr := catalog.Generate(cfg)
	results = append(results, Result{Name: "official model cache", Err: catalogErr})

	key, keyErr := cfg.ResolveCLIProxyAPIKey()
	results = append(results, Result{Name: "CLIProxyAPI key", Err: keyErr})
	if keyErr != nil {
		if e2e {
			results = append(results, Result{Name: "third-party model e2e", Err: fmt.Errorf("skipped: CLIProxyAPI key unavailable")})
		}
		return results
	}

	readyErr := r.checkModels(ctx, cfg, key)
	results = append(results, Result{Name: "CLIProxyAPI readiness", Err: readyErr})
	if e2e {
		if readyErr != nil {
			results = append(results, Result{Name: "third-party model e2e", Err: fmt.Errorf("skipped: CLIProxyAPI unavailable")})
		} else {
			results = append(results, Result{Name: "third-party model e2e", Err: r.checkE2E(ctx, cfg, key, modelID)})
		}
	}
	return results
}

func (r Runner) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r Runner) checkModels(ctx context.Context, cfg config.Config, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(cfg.CLIProxyBaseURL, "models"), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("models endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r Runner) checkE2E(ctx context.Context, cfg config.Config, key, requested string) error {
	model, err := selectModel(cfg, requested)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"model":             model.UpstreamModel,
		"input":             "Reply with exactly: OK",
		"max_output_tokens": 16,
		"stream":            false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(cfg.CLIProxyBaseURL, "responses"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("responses endpoint returned HTTP %d", resp.StatusCode)
	}
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return fmt.Errorf("responses endpoint returned an invalid JSON body")
	}
	return nil
}

func endpoint(baseURL, resource string) string {
	return strings.TrimRight(baseURL, "/") + "/" + resource
}

func selectModel(cfg config.Config, requested string) (config.ModelSpec, error) {
	requested = strings.TrimPrefix(requested, cfg.ModelPrefix)
	for _, model := range cfg.Models {
		if model.Compatibility.Status == "unsupported" {
			continue
		}
		if requested == "" || model.ID == requested {
			return model, nil
		}
	}
	if requested == "" {
		return config.ModelSpec{}, fmt.Errorf("no enabled third-party model is configured")
	}
	return config.ModelSpec{}, fmt.Errorf("third-party model %q is not configured", requested)
}
