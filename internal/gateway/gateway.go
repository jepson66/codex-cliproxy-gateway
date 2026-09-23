package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"codex-cliproxy-gateway/internal/catalog"
	"codex-cliproxy-gateway/internal/config"
	"github.com/klauspost/compress/zstd"
)

const maxRequestBody = 64 << 20

type Server struct {
	cfg          config.Config
	logger       *slog.Logger
	official     *httputil.ReverseProxy
	cliproxy     *httputil.ReverseProxy
	cliproxyKey  string
	decodeZstd   func([]byte) ([]byte, error)
	catalog      catalog.Document
	modelSpecs   map[string]config.ModelSpec
	sidecar      *exec.Cmd
	sidecarMutex sync.Mutex
}

func New(cfg config.Config, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	officialURL, err := url.Parse(cfg.OfficialBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse official_base_url: %w", err)
	}
	cliproxyURL, err := url.Parse(cfg.CLIProxyBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse cliproxy_base_url: %w", err)
	}
	doc, err := catalog.Generate(cfg)
	if err != nil {
		return nil, err
	}
	cliproxyKey, _ := cfg.ResolveCLIProxyAPIKey()
	s := &Server{
		cfg:         cfg,
		logger:      logger,
		cliproxyKey: cliproxyKey,
		decodeZstd:  decompressZstd,
		catalog:     doc,
		modelSpecs:  make(map[string]config.ModelSpec),
	}
	for _, model := range cfg.Models {
		if model.Compatibility.Status != "unsupported" {
			s.modelSpecs[cfg.ModelPrefix+model.ID] = model
		}
	}
	s.official = s.newReverseProxy(officialURL, false)
	s.cliproxy = s.newReverseProxy(cliproxyURL, true)
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.live)
	mux.HandleFunc("GET /livez", s.live)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /models", s.models)
	mux.HandleFunc("GET /v1/models", s.openAIModels)
	mux.HandleFunc("/", s.route)
	return mux
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.startSidecar(ctx); err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("gateway listening", "address", s.cfg.Listen)
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		s.stopSidecar()
		return nil
	case err := <-errCh:
		s.stopSidecar()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"models": len(s.catalog.Models),
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.cliproxyKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "cliproxy key unavailable"})
		return
	}
	if err := s.checkCLIProxy(r.Context()); err != nil {
		s.logger.Warn("readiness check failed", "error_class", "cliproxy_unavailable")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "cliproxy unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "models": len(s.catalog.Models)})
}

func (s *Server) checkCLIProxy(ctx context.Context) error {
	target := strings.TrimRight(s.cfg.CLIProxyBaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cliproxyKey)
	client := &http.Client{Transport: s.cliproxy.Transport, Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("CLIProxyAPI models status %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.catalog)
}

func (s *Server) openAIModels(w http.ResponseWriter, _ *http.Request) {
	data := make([]map[string]any, 0, len(s.catalog.Models))
	for _, model := range s.catalog.Models {
		slug, _ := model["slug"].(string)
		if slug == "" {
			continue
		}
		data = append(data, map[string]any{
			"id":       slug,
			"object":   "model",
			"created":  0,
			"owned_by": "codex-cliproxy-gateway",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.official.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	routingBody := body
	compressed := hasContentEncoding(r.Header, "zstd")
	if compressed {
		routingBody, err = s.decodeZstd(body)
		if err != nil {
			http.Error(w, "unable to decode zstd request body", http.StatusBadRequest)
			return
		}
	}

	model, rewritten, thirdParty, err := routeBody(routingBody, s.cfg.ModelPrefix, s.modelSpecs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if thirdParty {
		if s.cliproxyKey == "" {
			http.Error(w, "CLIProxyAPI key is not configured; run import-cliproxy-key", http.StatusServiceUnavailable)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(rewritten))
		r.ContentLength = int64(len(rewritten))
		r.Header.Del("Content-Encoding")
		r.Header.Set("Content-Length", fmt.Sprintf("%d", len(rewritten)))
		s.serveLogged(w, r, s.cliproxy, "cliproxy", model)
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	s.serveLogged(w, r, s.official, "official", model)
}

func (s *Server) serveLogged(w http.ResponseWriter, r *http.Request, handler http.Handler, route, model string) {
	requestID := newRequestID()
	w.Header().Set("X-Codex-Cliproxy-Request-ID", requestID)
	r.Header.Set("X-Codex-Cliproxy-Request-ID", requestID)
	recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	started := time.Now()
	handler.ServeHTTP(recorder, r)
	s.logger.Info("request completed",
		"request_id", requestID,
		"route", route,
		"model", model,
		"status", recorder.status,
		"duration_ms", time.Since(started).Milliseconds(),
	)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}

func hasContentEncoding(headers http.Header, expected string) bool {
	for _, value := range headers.Values("Content-Encoding") {
		for _, encoding := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(encoding), expected) {
				return true
			}
		}
	}
	return false
}

func decompressZstd(body []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(bytes.NewReader(body),
		zstd.WithDecoderMaxMemory(uint64(maxRequestBody)),
		zstd.WithDecoderConcurrency(1),
	)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	decoded, readErr := io.ReadAll(io.LimitReader(decoder, maxRequestBody+1))
	if int64(len(decoded)) > maxRequestBody {
		return nil, errors.New("decompressed request body is too large")
	}
	if readErr != nil {
		return nil, readErr
	}
	return decoded, nil
}

func routeBody(body []byte, prefix string, models map[string]config.ModelSpec) (model string, rewritten []byte, thirdParty bool, err error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", body, false, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, false, fmt.Errorf("request body must be JSON: %w", err)
	}
	if rawModel, ok := payload["model"]; ok {
		_ = json.Unmarshal(rawModel, &model)
	}
	if !strings.HasPrefix(model, prefix) {
		return model, body, false, nil
	}
	spec, ok := models[model]
	if !ok {
		return model, nil, false, fmt.Errorf("third-party model %q is not configured", model)
	}
	rewrittenModel, _ := json.Marshal(spec.UpstreamModel)
	payload["model"] = rewrittenModel
	rewritten, err = json.Marshal(payload)
	if err != nil {
		return model, nil, false, fmt.Errorf("rewrite model: %w", err)
	}
	return model, rewritten, true, nil
}

func (s *Server) newReverseProxy(target *url.URL, thirdParty bool) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Transport: defaultTransport(),
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Codex calls the configured base URL under /v1. Both upstream base
			// URLs already include their own API root, so remove only the local
			// gateway prefix before joining paths in SetURL.
			pr.Out.URL.Path = trimAPIPrefix(pr.Out.URL.Path)
			pr.Out.URL.RawPath = ""
			pr.SetURL(target)
			pr.SetXForwarded()
			removeHopByHop(pr.Out.Header)
			if thirdParty {
				stripSensitiveHeaders(pr.Out.Header)
				if s.cliproxyKey != "" {
					pr.Out.Header.Set("Authorization", "Bearer "+s.cliproxyKey)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.logger.Error("upstream request failed", "error", err)
			http.Error(w, "gateway upstream request failed", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
	return proxy
}

func trimAPIPrefix(path string) string {
	if path == "/v1" {
		return "/"
	}
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}

func stripSensitiveHeaders(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "cookie" ||
			strings.HasPrefix(lower, "chatgpt-") || strings.HasPrefix(lower, "openai-") ||
			strings.HasPrefix(lower, "x-chatgpt-") || strings.HasPrefix(lower, "x-openai-") {
			headers.Del(name)
		}
	}
}

func defaultTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.IdleConnTimeout = 120 * time.Second
	return transport
}

func removeHopByHop(headers http.Header) {
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		headers.Del(name)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) startSidecar(ctx context.Context) error {
	if !s.cfg.Sidecar.Enabled {
		return nil
	}
	s.sidecarMutex.Lock()
	defer s.sidecarMutex.Unlock()
	cmd := exec.CommandContext(ctx, s.cfg.Sidecar.Command, s.cfg.Sidecar.Args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start CLIProxyAPI sidecar: %w", err)
	}
	s.sidecar = cmd
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			s.logger.Error("CLIProxyAPI sidecar exited", "error", err)
		}
	}()
	return waitReady(ctx, s.cfg.Sidecar.ReadyURL, 15*time.Second)
}

func waitReady(ctx context.Context, readyURL string, timeout time.Duration) error {
	if readyURL == "" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("CLIProxyAPI sidecar did not become ready at %s", readyURL)
}

func (s *Server) stopSidecar() {
	s.sidecarMutex.Lock()
	defer s.sidecarMutex.Unlock()
	if s.sidecar != nil && s.sidecar.Process != nil {
		_ = s.sidecar.Process.Signal(os.Interrupt)
	}
}
