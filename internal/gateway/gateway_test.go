package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
	"github.com/klauspost/compress/zstd"
)

type receivedRequest struct {
	path   string
	header http.Header
	body   []byte
}

func TestOfficialRoutePreservesOAuthAndStreams(t *testing.T) {
	received := make(chan receivedRequest, 1)
	officialTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		received <- receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
		return testResponse(http.StatusOK, "text/event-stream", "data: first\n\ndata: second\n\n"), nil
	})

	cliproxyCalled := false
	cliproxyTransport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		cliproxyCalled = true
		return testResponse(http.StatusInternalServerError, "text/plain", "unexpected"), nil
	})

	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.official.Transport = officialTransport
	server.cliproxy.Transport = cliproxyTransport
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer chatgpt-oauth")
	request.Header.Set("ChatGPT-Account-ID", "account-123")
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Body.String(); got != "data: first\n\ndata: second\n\n" {
		t.Fatalf("stream body = %q", got)
	}
	upstream := <-received
	if upstream.path != "/backend-api/codex/responses" {
		t.Fatalf("official path = %q", upstream.path)
	}
	if got := upstream.header.Get("Authorization"); got != "Bearer chatgpt-oauth" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := upstream.header.Get("ChatGPT-Account-ID"); got != "account-123" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	if cliproxyCalled {
		t.Fatal("CLIProxyAPI was called for an official model")
	}
}

func TestThirdPartyRouteStripsOAuthAndRewritesModel(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	officialCalled := false
	officialTransport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		officialCalled = true
		return testResponse(http.StatusInternalServerError, "text/plain", "unexpected"), nil
	})

	received := make(chan receivedRequest, 1)
	cliproxyTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		received <- receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.official.Transport = officialTransport
	server.cliproxy.Transport = cliproxyTransport
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello","metadata":{"large":9007199254740993}}`))
	request.Header.Set("Authorization", "Bearer chatgpt-oauth")
	request.Header.Set("ChatGPT-Account-ID", "account-123")
	request.Header.Set("Cookie", "session=secret")
	request.Header.Set("OpenAI-Organization", "org-secret")
	request.Header.Set("OpenAI-Project", "project-secret")
	request.Header.Set("OpenAI-Actor-Authorization", "actor-secret")
	request.Header.Set("X-OpenAI-Future-Credential", "future-secret")
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	upstream := <-received
	if upstream.path != "/v1/responses" {
		t.Fatalf("CLIProxyAPI path = %q", upstream.path)
	}
	if got := upstream.header.Get("Authorization"); got != "Bearer local-proxy-key" {
		t.Fatalf("Authorization = %q", got)
	}
	for _, name := range []string{"ChatGPT-Account-ID", "Cookie", "OpenAI-Organization", "OpenAI-Project", "OpenAI-Actor-Authorization", "X-OpenAI-Future-Credential"} {
		if got := upstream.header.Get(name); got != "" {
			t.Fatalf("%s leaked: %q", name, got)
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(upstream.body, &payload); err != nil {
		t.Fatal(err)
	}
	var model string
	_ = json.Unmarshal(payload["model"], &model)
	if model != "kimi-k3" {
		t.Fatalf("rewritten model = %q", model)
	}
	if !strings.Contains(string(payload["metadata"]), "9007199254740993") {
		t.Fatalf("large JSON number was changed: %s", payload["metadata"])
	}
	if officialCalled {
		t.Fatal("official upstream was called for a prefixed model")
	}
	if recorder.Header().Get("X-Codex-Cliproxy-Request-ID") == "" {
		t.Fatal("response has no request correlation id")
	}
}

func TestUnknownThirdPartyModelFailsClosed(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	called := false
	server.cliproxy.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return testResponse(http.StatusOK, "application/json", `{}`), nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/not-configured","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("unknown model reached CLIProxyAPI")
	}
}

func TestConfiguredUpstreamModelIsUsed(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	spec := server.modelSpecs["cliproxy/kimi-k3"]
	spec.UpstreamModel = "moonshot-upstream-id"
	server.modelSpecs["cliproxy/kimi-k3"] = spec
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"moonshot-upstream-id"`) {
			t.Fatalf("body = %s", body)
		}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReadinessChecksAuthenticatedCLIProxy(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer local-proxy-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		return testResponse(http.StatusOK, "application/json", `{"data":[]}`), nil
	})
	request, _ := http.NewRequest(http.MethodGet, "http://gateway.test/readyz", nil)
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestModelsEndpointContainsMergedCatalog(t *testing.T) {
	server := newTestServer(t, "http://127.0.0.1:1/backend-api/codex", "http://127.0.0.1:2/v1")
	request, _ := http.NewRequest(http.MethodGet, "http://gateway.test/v1/models", nil)
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bufio.NewReader(recorder.Body)).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, item := range envelope.Data {
		ids[item.ID] = true
	}
	for _, id := range []string{"gpt-test", "cliproxy/kimi-k3"} {
		if !ids[id] {
			t.Fatalf("model %q missing from %#v", id, ids)
		}
	}
}

func TestZstdRoutingPassesOfficialThroughAndDecodesThirdParty(t *testing.T) {
	t.Run("official", func(t *testing.T) {
		received := make(chan receivedRequest, 1)
		server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
		server.decodeZstd = func(body []byte) ([]byte, error) {
			if string(body) != "compressed-official" {
				t.Fatalf("compressed body = %q", body)
			}
			return []byte(`{"model":"gpt-test","input":"hello"}`), nil
		}
		server.official.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			received <- receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
			return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
		})
		request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader("compressed-official"))
		request.Header.Set("Content-Encoding", "zstd")
		recorder := newResponseRecorder()
		server.Handler().ServeHTTP(recorder, request)
		upstream := <-received
		if string(upstream.body) != "compressed-official" {
			t.Fatalf("official body was changed: %q", upstream.body)
		}
		if got := upstream.header.Get("Content-Encoding"); got != "zstd" {
			t.Fatalf("official Content-Encoding = %q", got)
		}
	})

	t.Run("third party", func(t *testing.T) {
		t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
		received := make(chan receivedRequest, 1)
		server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
		server.decodeZstd = func(body []byte) ([]byte, error) {
			if string(body) != "compressed-third-party" {
				t.Fatalf("compressed body = %q", body)
			}
			return []byte(`{"model":"cliproxy/kimi-k3","input":"hello"}`), nil
		}
		server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			received <- receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
			return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
		})
		request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader("compressed-third-party"))
		request.Header.Set("Content-Encoding", "zstd")
		recorder := newResponseRecorder()
		server.Handler().ServeHTTP(recorder, request)
		upstream := <-received
		if got := upstream.header.Get("Content-Encoding"); got != "" {
			t.Fatalf("CLIProxyAPI Content-Encoding = %q", got)
		}
		if !strings.Contains(string(upstream.body), `"model":"kimi-k3"`) {
			t.Fatalf("CLIProxyAPI body = %s", upstream.body)
		}
	})
}

func TestEmbeddedZstdDecoder(t *testing.T) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { encoder.Close() })
	want := []byte(`{"model":"cliproxy/kimi-k3","input":"hello"}`)
	compressed := encoder.EncodeAll(want, nil)
	got, err := decompressZstd(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded body = %q, want %q", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testResponse(status int, contentType, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type responseRecorder struct {
	HeaderMap http.Header
	Body      *bytes.Buffer
	Code      int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{HeaderMap: make(http.Header), Body: new(bytes.Buffer), Code: http.StatusOK}
}

func (r *responseRecorder) Header() http.Header { return r.HeaderMap }

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.Body.Write(data)
}

func (r *responseRecorder) WriteHeader(status int) { r.Code = status }

func (r *responseRecorder) Flush() {}

func newTestServer(t *testing.T, officialURL, cliproxyURL string) *Server {
	t.Helper()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "models_cache.json")
	cache := `{"models":[{"slug":"gpt-test","display_name":"GPT Test","description":"official","context_window":1000,"supported_reasoning_levels":[{"effort":"medium","description":"medium"}],"default_reasoning_level":"medium","input_modalities":["text"]}]}`
	if err := os.WriteFile(cachePath, []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OfficialBaseURL = officialURL
	cfg.CLIProxyBaseURL = cliproxyURL
	cfg.OfficialModelsCache = cachePath
	cfg.ModelCatalogPath = filepath.Join(dir, "catalog.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
