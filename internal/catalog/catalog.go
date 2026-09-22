package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex-cliproxy-gateway/internal/config"
)

type Document struct {
	Models []map[string]any `json:"models"`
}

const thirdPartyIdentityPreamble = "You are the underlying model selected by the user, operating as an AI coding agent inside the Codex host. If asked about your identity, report that underlying model and provider truthfully, and distinguish them from the Codex host. You and the user share one workspace, and your job is to collaborate with them until their intended goal is completely handled."

func Generate(cfg config.Config) (Document, error) {
	data, err := os.ReadFile(cfg.OfficialModelsCache)
	if err != nil {
		return Document{}, fmt.Errorf("read official model cache %s: %w", cfg.OfficialModelsCache, err)
	}
	var envelope struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Document{}, fmt.Errorf("parse official model cache: %w", err)
	}
	if len(envelope.Models) == 0 {
		return Document{}, fmt.Errorf("official model cache has no models")
	}

	models := make([]map[string]any, 0, len(envelope.Models)+len(cfg.Models))
	for _, model := range envelope.Models {
		models = append(models, clone(model))
	}
	template := envelope.Models[0]
	for index, spec := range cfg.Models {
		model := clone(template)
		model["model_messages"] = thirdPartyModelMessages(model["model_messages"])
		model["slug"] = cfg.ModelPrefix + spec.ID
		model["display_name"] = nonEmpty(spec.DisplayName, spec.ID)
		model["description"] = nonEmpty(spec.Description, spec.DisplayName+" via CLIProxyAPI")
		model["visibility"] = "list"
		model["supported_in_api"] = true
		model["priority"] = 1000 + index
		model["additional_speed_tiers"] = []any{}
		model["service_tiers"] = []any{}
		model["availability_nux"] = nil
		model["upgrade"] = nil
		model["include_skills_usage_instructions"] = false
		model["include_plugin_usage_instructions"] = false
		model["include_apps_usage_instructions"] = false
		model["default_reasoning_summary"] = "none"
		model["support_verbosity"] = false
		model["default_verbosity"] = "low"
		model["supports_search_tool"] = false
		model["use_responses_lite"] = false
		model["node_repl_auto_review_required"] = false
		model["node_repl_disabled"] = false
		if spec.ContextWindow > 0 {
			model["context_window"] = spec.ContextWindow
			model["max_context_window"] = spec.ContextWindow
		}
		if len(spec.InputModalities) > 0 {
			model["input_modalities"] = spec.InputModalities
		}
		levels := spec.ReasoningLevels
		if len(levels) == 0 {
			levels = []string{"none"}
		}
		reasoning := make([]map[string]string, 0, len(levels))
		for _, effort := range levels {
			reasoning = append(reasoning, map[string]string{
				"effort":      effort,
				"description": reasoningDescription(effort),
			})
		}
		model["supported_reasoning_levels"] = reasoning
		model["default_reasoning_level"] = nonEmpty(spec.DefaultReasoningLevel, levels[0])
		models = append(models, model)
	}

	return Document{Models: models}, nil
}

func thirdPartyModelMessages(value any) map[string]any {
	messages, _ := value.(map[string]any)
	if messages == nil {
		messages = map[string]any{}
	}
	instructions, _ := messages["instructions_template"].(string)
	messages["instructions_template"] = thirdPartyInstructions(instructions)
	return messages
}

func thirdPartyInstructions(instructions string) string {
	if instructions == "" {
		return thirdPartyIdentityPreamble
	}
	// The official catalog currently starts with a model-identity paragraph.
	// Replace that paragraph only when it is actually an official Codex/GPT
	// identity; otherwise keep the complete template and prepend our neutral
	// identity guidance. This makes catalog schema/content drift fail safe.
	firstParagraph := instructions
	remainder := ""
	if paragraphEnd := strings.Index(instructions, "\n\n"); paragraphEnd >= 0 {
		firstParagraph = instructions[:paragraphEnd]
		remainder = strings.TrimLeft(instructions[paragraphEnd+2:], "\n")
	}
	if strings.Contains(firstParagraph, "You are Codex") || strings.Contains(firstParagraph, "based on GPT") {
		instructions = remainder
	}
	instructions = strings.ReplaceAll(instructions, "As Codex,", "As an AI coding agent,")
	if instructions == "" {
		return thirdPartyIdentityPreamble
	}
	return thirdPartyIdentityPreamble + "\n\n" + instructions
}

func Write(cfg config.Config) error {
	doc, err := Generate(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.ModelCatalogPath), 0o700); err != nil {
		return fmt.Errorf("create model catalog directory: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(cfg.ModelCatalogPath, data, 0o600); err != nil {
		return fmt.Errorf("write model catalog: %w", err)
	}
	return nil
}

func IDs(doc Document) []string {
	ids := make([]string, 0, len(doc.Models))
	for _, model := range doc.Models {
		if slug, ok := model["slug"].(string); ok {
			ids = append(ids, slug)
		}
	}
	sort.Strings(ids)
	return ids
}

func clone(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func reasoningDescription(effort string) string {
	if effort == "none" {
		return "The upstream model manages reasoning automatically"
	}
	return "Reasoning effort: " + effort
}
