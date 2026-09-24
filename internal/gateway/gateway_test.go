package gateway

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol","instructions":"You are Codex, an agent based on GPT-5.","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>openai-docs: Use for self-knowledge when referring to Codex.</skills_instructions>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`))
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
	if !bytes.Contains(upstream.body, []byte("You are Codex, an agent based on GPT-5.")) {
		t.Fatalf("official model instructions were changed: %s", upstream.body)
	}
	if !bytes.Contains(upstream.body, []byte("openai-docs: Use for self-knowledge when referring to Codex.")) {
		t.Fatalf("official self-knowledge skill scope was changed: %s", upstream.body)
	}
	if cliproxyCalled {
		t.Fatal("CLIProxyAPI was called for an official model")
	}
}

func TestKimiToOfficialDropsForeignReasoningAndPreservesVisibleContext(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")

	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	officialBody := make(chan []byte, 1)
	server.official.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		officialBody <- body
		if bytes.Contains(body, []byte(`"type":"reasoning"`)) {
			return testResponse(http.StatusBadRequest, "application/json", `{"detail":"The encrypted content for item rs_foreign could not be verified. Reason: Encrypted content could not be decrypted or parsed."}`), nil
		}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	first, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{
		"model":"cliproxy/kimi-k3",
		"prompt_cache_key":"desktop-session-1",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}]
	}`))
	firstRecorder := newResponseRecorder()
	server.Handler().ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("Kimi status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
	}

	second, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{
		"model":"gpt-test",
		"prompt_cache_key":"desktop-session-1",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
			{"type":"reasoning","id":"rs_foreign","summary":[],"encrypted_content":"foreign-encrypted-value"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible Kimi reply"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}
		]
	}`))
	secondRecorder := newResponseRecorder()
	server.Handler().ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("official status = %d, body = %s", secondRecorder.Code, secondRecorder.Body.String())
	}

	forwarded := <-officialBody
	if bytes.Contains(forwarded, []byte(`"type":"reasoning"`)) || bytes.Contains(forwarded, []byte("foreign-encrypted-value")) {
		t.Fatalf("foreign reasoning reached official upstream: %s", forwarded)
	}
	for _, visible := range []string{"first", "visible Kimi reply", "second"} {
		if !bytes.Contains(forwarded, []byte(visible)) {
			t.Fatalf("visible context %q was removed: %s", visible, forwarded)
		}
	}
}

func TestOfficialRoutePreservesValidGPTReasoning(t *testing.T) {
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	validEncryptedContent := validGPTReasoningEncryptedContent()
	server.official.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(validEncryptedContent)) {
			t.Fatalf("valid GPT reasoning was removed: %s", body)
		}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	body := `{"model":"gpt-test","input":[{"type":"reasoning","id":"rs_gpt","summary":[],"encrypted_content":"` + validEncryptedContent + `"}]}`
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(body))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestThirdPartyRouteDropsUnsupportedCompactionAndKeepsOtherContext(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"type":"compaction"`)) {
			return testResponse(http.StatusBadRequest, "application/json", `{"error":{"message":"invalid_request_error: input.1: item type \"compaction\" is not supported"}}`), nil
		}
		if !bytes.Contains(body, []byte(`"type":"reasoning"`)) || !bytes.Contains(body, []byte("gpt-encrypted-value")) {
			t.Fatalf("reasoning input was unexpectedly removed from third-party route: %s", body)
		}
		for _, visible := range []string{"before compaction", "after compaction"} {
			if !bytes.Contains(body, []byte(visible)) {
				t.Fatalf("visible context %q was removed: %s", visible, body)
			}
		}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	body := `{"model":"cliproxy/kimi-k3","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"before compaction"}]},{"type":"compaction","id":"cmp_gpt","encrypted_content":"official-compaction"},{"type":"reasoning","id":"rs_gpt","summary":[],"encrypted_content":"gpt-encrypted-value"},{"type":"message","role":"user","content":[{"type":"input_text","text":"after compaction"}]}]}`
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(body))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
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
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello","reasoning":{"effort":"none","summary":"auto"},"metadata":{"large":9007199254740993}}`))
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
	if _, exists := payload["reasoning"]; exists {
		t.Fatalf("Codex reasoning object reached Kimi upstream: %s", payload["reasoning"])
	}
	var thinking map[string]json.RawMessage
	if err := json.Unmarshal(payload["thinking"], &thinking); err != nil {
		t.Fatal(err)
	}
	var thinkingType string
	if err := json.Unmarshal(thinking["type"], &thinkingType); err != nil {
		t.Fatal(err)
	}
	if thinkingType != "disabled" {
		t.Fatalf("translated thinking type = %q", thinkingType)
	}
	if _, exists := thinking["effort"]; exists {
		t.Fatalf("Kimi thinking request contains unsupported effort: %s", payload["thinking"])
	}
	if officialCalled {
		t.Fatal("official upstream was called for a prefixed model")
	}
	if recorder.Header().Get("X-Codex-Cliproxy-Request-ID") == "" {
		t.Fatal("response has no request correlation id")
	}
}

func TestThirdPartyRouteRemovesOfficialIdentityFromRuntimeInstructions(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	received := make(chan []byte, 1)
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	body := `{
		"model":"cliproxy/kimi-k3",
		"instructions":"You are Codex, an agent based on GPT-5. You and the user share one workspace.\n\nAs Codex, preserve this operational rule.",
		"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are running inside the Codex desktop app."}]}]
	}`
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(body))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	forwarded := <-received
	if bytes.Contains(forwarded, []byte("You are Codex")) || bytes.Contains(forwarded, []byte("based on GPT")) {
		t.Fatalf("official model identity reached third-party upstream: %s", forwarded)
	}
	for _, expected := range []string{
		"As an AI coding agent, preserve this operational rule.",
		"You are running inside the Codex desktop app.",
	} {
		if !bytes.Contains(forwarded, []byte(expected)) {
			t.Fatalf("expected context %q was changed: %s", expected, forwarded)
		}
	}
	for _, injectedIdentity := range []string{"Kimi", "Moonshot", "underlying model"} {
		if bytes.Contains(forwarded, []byte(injectedIdentity)) {
			t.Fatalf("third-party identity was injected into request: %s", forwarded)
		}
	}
}

func TestThirdPartyRouteNeutralizesOpenAISelfKnowledgeSkillTrigger(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	received := make(chan []byte, 1)
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	body := `{
		"model":"cliproxy/kimi-k3",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>\n- openai-docs: Use for Codex models/pricing, settings, setup, troubleshooting, and self-knowledge—including 'you,' 'your,' 'this app,' or 'this coding agent' when they refer to Codex—and for OpenAI APIs/products. Do not use for generic app/software tasks that merely mention Codex.\n- skill-creator: Create or update a Codex skill.\n</skills_instructions>"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"你是谁"}]}
		]
	}`
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(body))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	forwarded := <-received
	for _, leaked := range []string{"self-knowledge", "'you,' 'your,' 'this app,' or 'this coding agent'"} {
		if bytes.Contains(forwarded, []byte(leaked)) {
			t.Fatalf("OpenAI self-identity skill trigger reached third-party upstream: %s", forwarded)
		}
	}
	for _, preserved := range []string{
		"openai-docs: Use for Codex models/pricing, settings, setup, troubleshooting",
		"and for OpenAI APIs/products",
		"skill-creator: Create or update a Codex skill.",
		"你是谁",
	} {
		if !bytes.Contains(forwarded, []byte(preserved)) {
			t.Fatalf("provider-neutral context %q was changed: %s", preserved, forwarded)
		}
	}
}

func TestThirdPartyRouteFiltersConfiguredAppNamespacesAndKeepsCoreTools(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Tools) != 2 {
			t.Fatalf("forwarded tools = %#v", payload.Tools)
		}
		for _, tool := range payload.Tools {
			if strings.HasPrefix(tool.Name, "mcp__codex_apps__") {
				t.Fatalf("excluded app namespace reached upstream: %#v", tool)
			}
		}
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})

	body := `{"model":"cliproxy/kimi-k3","input":"hello","tools":[{"type":"function","name":"exec_command"},{"type":"namespace","name":"mcp__codex_apps__notion","tools":[]},{"type":"namespace","name":"mcp__cua_repl","tools":[]}]}`
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(body))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestModelSwitchAnnotationAttributesEarlierAssistantToPreviousModel(t *testing.T) {
	body := []byte(`{"model":"gpt-test","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我是 Kimi"}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"<model_switch>\nThe user was previously using a different model.\nYou are Codex.\n</model_switch>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"你是谁"}]}]}`)
	rewritten, changed, err := annotateModelSwitch(body)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("model switch was not annotated")
	}
	if !bytes.Contains(rewritten, []byte(modelSwitchAttribution)) {
		t.Fatalf("annotation missing: %s", rewritten)
	}
	if !bytes.Contains(rewritten, []byte("我是 Kimi")) {
		t.Fatalf("visible assistant context changed: %s", rewritten)
	}
	rewrittenAgain, changedAgain, err := annotateModelSwitch(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if changedAgain || !bytes.Equal(rewrittenAgain, rewritten) {
		t.Fatalf("annotation is not idempotent: %s", rewrittenAgain)
	}
}

func TestThirdPartySSEOmitsEchoedToolSchemasAndPreservesOutput(t *testing.T) {
	input := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1","tools":[{"type":"function","name":"large","description":"schema"}],"output":[]}}` + "\n\n" +
		`data: {"type":"response.output_text.delta","delta":"OK"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"id":"resp_1","tools":[{"type":"function","name":"large"}],"output":[{"type":"message"}]}}` + "\n\n"
	body := sanitizeThirdPartySSE(io.NopCloser(strings.NewReader(input)))
	defer body.Close()
	output, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte(`"name":"large"`)) || bytes.Contains(output, []byte(`"description":"schema"`)) {
		t.Fatalf("echoed tool schema was preserved: %s", output)
	}
	if !bytes.Contains(output, []byte(`"delta":"OK"`)) || !bytes.Contains(output, []byte(`"output":[{"type":"message"}]`)) {
		t.Fatalf("response output changed: %s", output)
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

func TestDeprecatedKimiCode256KModelFailsClosed(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	called := false
	server.cliproxy.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3-256k","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "not configured") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("deprecated 256K model reached CLIProxyAPI")
	}
}

func TestMissingKimiCodeLoginReturnsActionableError(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	enableKimiCodeAuth(server, "cliproxy/kimi-k3")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected CLIProxyAPI request: %s %s", r.Method, r.URL.Path)
		}
		return testResponse(http.StatusOK, "application/json", `{"data":[{"id":"other-model"}]}`), nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{"provider_auth_required", "codex-cliproxy-gateway login kimi-code", "authorize this device", "https://www.kimi.com/code", "API key", server.cfg.CLIProxyConfigPath} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("body missing %q: %s", expected, recorder.Body.String())
		}
	}
}

func TestAuthenticatedKimiMissingRequestedAliasIsNotReportedAsLoginFailure(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	spec := server.modelSpecs["cliproxy/kimi-k3"]
	spec.ID = "kimi-test-alias"
	spec.UpstreamModel = "kimi-test-alias"
	server.modelSpecs["cliproxy/kimi-test-alias"] = spec
	enableKimiCodeAuth(server, "cliproxy/kimi-test-alias")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected CLIProxyAPI request: %s %s", r.Method, r.URL.Path)
		}
		return testResponse(http.StatusOK, "application/json", `{"data":[{"id":"kimi-k3"}]}`), nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-test-alias","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "provider_model_unavailable") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "login kimi-code") {
		t.Fatalf("missing alias was misreported as login failure: %s", recorder.Body.String())
	}
}

func TestInvalidKimiCodeCredentialReturnsActionableError(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	enableKimiCodeAuth(server, "cliproxy/kimi-k3")
	server.cliproxy.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return testResponse(http.StatusOK, "application/json", `{"data":[{"id":"kimi-k3"}]}`), nil
		}
		resp := testResponse(http.StatusUnauthorized, "application/json", `{"error":{"message":"upstream leaked detail"}}`)
		resp.Request = r
		return resp, nil
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "kimi_code_credentials_rejected") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "upstream leaked detail") {
		t.Fatalf("upstream error leaked: %s", recorder.Body.String())
	}
}

func TestKimiCodePreflightKeepsCLIProxyOutageDistinctFromLogin(t *testing.T) {
	t.Setenv("CLIPROXY_API_KEY", "local-proxy-key")
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	enableKimiCodeAuth(server, "cliproxy/kimi-k3")
	server.cliproxy.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader(`{"model":"cliproxy/kimi-k3","input":"hello"}`))
	recorder := newResponseRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || strings.Contains(recorder.Body.String(), "login kimi-code") {
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

func TestCLIProxyAllowsSlowModelResponseHeaders(t *testing.T) {
	server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
	officialTransport, ok := server.official.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("official transport type = %T", server.official.Transport)
	}
	cliproxyTransport, ok := server.cliproxy.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("CLIProxy transport type = %T", server.cliproxy.Transport)
	}
	if officialTransport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("official response header timeout = %s", officialTransport.ResponseHeaderTimeout)
	}
	if cliproxyTransport.ResponseHeaderTimeout != 2*time.Minute {
		t.Fatalf("CLIProxy response header timeout = %s, want 2m", cliproxyTransport.ResponseHeaderTimeout)
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

	t.Run("official with incompatible reasoning", func(t *testing.T) {
		received := make(chan receivedRequest, 1)
		server := newTestServer(t, "https://official.test/backend-api/codex", "http://cliproxy.test/v1")
		server.decodeZstd = func(body []byte) ([]byte, error) {
			if string(body) != "compressed-official-with-kimi-reasoning" {
				t.Fatalf("compressed body = %q", body)
			}
			return []byte(`{"model":"gpt-test","input":[{"type":"reasoning","id":"rs_kimi","encrypted_content":"foreign"},{"type":"message","role":"user","content":[{"type":"input_text","text":"visible"}]}]}`), nil
		}
		server.official.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			received <- receivedRequest{path: r.URL.Path, header: r.Header.Clone(), body: body}
			return testResponse(http.StatusOK, "text/event-stream", "data: ok\n\n"), nil
		})
		request, _ := http.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", strings.NewReader("compressed-official-with-kimi-reasoning"))
		request.Header.Set("Content-Encoding", "zstd")
		recorder := newResponseRecorder()
		server.Handler().ServeHTTP(recorder, request)
		upstream := <-received
		if got := upstream.header.Get("Content-Encoding"); got != "" {
			t.Fatalf("official sanitized Content-Encoding = %q", got)
		}
		if bytes.Contains(upstream.body, []byte(`"type":"reasoning"`)) {
			t.Fatalf("foreign reasoning reached official upstream: %s", upstream.body)
		}
		if !bytes.Contains(upstream.body, []byte("visible")) {
			t.Fatalf("visible context was removed: %s", upstream.body)
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

func validGPTReasoningEncryptedContent() string {
	decoded := make([]byte, 73)
	decoded[0] = 0x80
	return base64.RawURLEncoding.EncodeToString(decoded)
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
	for i := range cfg.Models {
		cfg.Models[i].ProviderID = ""
	}
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

func enableKimiCodeAuth(server *Server, modelID string) {
	spec := server.modelSpecs[modelID]
	spec.ProviderID = "kimi-code"
	server.modelSpecs[modelID] = spec
}
