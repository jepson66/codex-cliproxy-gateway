package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
)

func TestGeneratePreservesOfficialModelsAndAddsKimiCode(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "models_cache.json")
	official := map[string]any{
		"models": []any{
			map[string]any{
				"slug":                       "gpt-one",
				"display_name":               "GPT One",
				"custom":                     "keep-me",
				"multi_agent_version":        "future-value-must-not-leak",
				"supported_reasoning_levels": []any{},
				"model_messages": map[string]any{
					"instructions_template":   "You are Codex, an agent based on GPT-5. You and the user share one workspace.\n\nAs Codex, stay helpful.",
					"persistent_instructions": "preserve operational guidance",
				},
			},
			map[string]any{"slug": "gpt-two", "display_name": "GPT Two", "priority": 42},
		},
	}
	data, _ := json.Marshal(official)
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OfficialModelsCache = cachePath

	doc, err := Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 3 {
		t.Fatalf("model count = %d", len(doc.Models))
	}
	if doc.Models[0]["custom"] != "keep-me" || doc.Models[1]["slug"] != "gpt-two" {
		t.Fatalf("official entries changed: %#v", doc.Models[:2])
	}
	wantLevels := []map[string]string{
		{"effort": "none", "description": "The upstream model manages reasoning automatically"},
	}
	for index, slug := range []string{"cliproxy/kimi-k3"} {
		kimi := doc.Models[2+index]
		if kimi["slug"] != slug {
			t.Fatalf("Kimi slug = %#v, want %q", kimi["slug"], slug)
		}
		levels, ok := kimi["supported_reasoning_levels"].([]map[string]string)
		if !ok || !reflect.DeepEqual(levels, wantLevels) {
			t.Fatalf("Kimi reasoning levels = %#v", kimi["supported_reasoning_levels"])
		}
		if kimi["default_reasoning_level"] != "none" {
			t.Fatalf("Kimi default reasoning = %#v", kimi["default_reasoning_level"])
		}
		if _, ok := kimi["custom"]; ok {
			t.Fatalf("Kimi inherited an unknown official field: %#v", kimi["custom"])
		}
		if _, ok := kimi["multi_agent_version"]; ok {
			t.Fatalf("Kimi inherited an undeclared capability: %#v", kimi["multi_agent_version"])
		}
	}
	kimi := doc.Models[2]
	messages, ok := kimi["model_messages"].(map[string]any)
	if !ok {
		t.Fatalf("Kimi instructions template = %#v", kimi["model_messages"])
	}
	instructions, _ := messages["instructions_template"].(string)
	for _, identityPrompt := range []string{
		"based on GPT",
		"As Codex",
		"underlying model",
		"report that underlying model and provider truthfully",
		"Kimi",
		"Moonshot",
	} {
		if strings.Contains(instructions, identityPrompt) {
			t.Fatalf("Kimi instructions contain identity prompt %q: %q", identityPrompt, instructions)
		}
	}
	if instructions != "As an AI coding agent, stay helpful." {
		t.Fatalf("Kimi operational instructions changed: %q", instructions)
	}
	if messages["persistent_instructions"] != "preserve operational guidance" {
		t.Fatalf("Kimi operational model messages changed: %#v", messages)
	}
	officialMessages := doc.Models[0]["model_messages"].(map[string]any)
	if officialMessages["instructions_template"] != "You are Codex, an agent based on GPT-5. You and the user share one workspace.\n\nAs Codex, stay helpful." {
		t.Fatalf("official instructions changed: %#v", officialMessages)
	}
}

func TestThirdPartyInstructionsPreserveUnknownTemplatePreamble(t *testing.T) {
	input := "Follow the host security policy exactly.\n\nAs Codex, keep working until the task is complete."
	got := thirdPartyInstructions(input)
	want := "Follow the host security policy exactly.\n\nAs an AI coding agent, keep working until the task is complete."
	if got != want {
		t.Fatalf("third-party instructions = %q, want %q", got, want)
	}
}

func TestThirdPartyModelMessagesWithoutOfficialTemplate(t *testing.T) {
	messages := thirdPartyModelMessages(nil)
	if len(messages) != 1 {
		t.Fatalf("unexpected generated messages: %#v", messages)
	}
	got, _ := messages["instructions_template"].(string)
	if got != "" {
		t.Fatalf("generated instructions = %q", got)
	}
	for _, identityPrompt := range []string{"GPT", "Codex", "Kimi", "Moonshot", "underlying model"} {
		if strings.Contains(got, identityPrompt) {
			t.Fatalf("generated instructions contain identity prompt %q: %q", identityPrompt, got)
		}
	}
}
