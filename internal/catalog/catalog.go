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
	templateMessages := envelope.Models[0]["model_messages"]
	for index, spec := range cfg.Models {
		if spec.Compatibility.Status == "unsupported" {
			continue
		}
		models = append(models, thirdPartyModel(cfg, spec, templateMessages, index))
	}

	return Document{Models: models}, nil
}

func thirdPartyModel(cfg config.Config, spec config.ModelSpec, templateMessages any, index int) map[string]any {
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

	model := map[string]any{
		"slug":                              cfg.ModelPrefix + spec.ID,
		"display_name":                      nonEmpty(spec.DisplayName, spec.ID),
		"description":                       nonEmpty(spec.Description, spec.DisplayName+" via CLIProxyAPI"),
		"visibility":                        "list",
		"supported_in_api":                  true,
		"priority":                          1000 + index,
		"additional_speed_tiers":            []any{},
		"service_tiers":                     []any{},
		"availability_nux":                  nil,
		"upgrade":                           nil,
		"include_skills_usage_instructions": false,
		"include_plugin_usage_instructions": false,
		"include_apps_usage_instructions":   false,
		"default_reasoning_summary":         "none",
		"support_verbosity":                 false,
		"default_verbosity":                 "low",
		"apply_patch_tool_type":             "freeform",
		"shell_type":                        "unified_exec",
		"tool_mode":                         "",
		"web_search_tool_type":              "text",
		"truncation_policy":                 map[string]any{"mode": "tokens", "limit": 10000},
		"supports_image_detail_original":    false,
		"context_window":                    spec.ContextWindow,
		"max_context_window":                spec.ContextWindow,
		"effective_context_window_percent":  95,
		"experimental_supported_tools":      []any{},
		"input_modalities":                  spec.InputModalities,
		"supports_search_tool":              spec.Capabilities.WebSearch,
		"use_responses_lite":                false,
		"node_repl_auto_review_required":    false,
		"node_repl_disabled":                !spec.Capabilities.Tools,
		"supported_reasoning_levels":        reasoning,
		"default_reasoning_level":           nonEmpty(spec.DefaultReasoningLevel, levels[0]),
		"model_messages":                    thirdPartyModelMessages(templateMessages),
	}
	return model
}

func thirdPartyModelMessages(value any) map[string]any {
	messages, _ := value.(map[string]any)
	filtered := make(map[string]any)
	for _, key := range []string{
		"approvals", "auto_review", "collaboration_modes", "confirmation_policies",
		"guardian_v2", "instructions_variables", "multi_agent", "permissions",
		"persistent_instructions", "token_budget",
	} {
		if item, ok := messages[key]; ok {
			filtered[key] = cloneValue(item)
		}
	}
	instructions, _ := messages["instructions_template"].(string)
	filtered["instructions_template"] = SanitizeThirdPartyInstructions(instructions)
	return filtered
}

// SanitizeThirdPartyInstructions removes identity claims that belong to an
// official Codex/GPT model while preserving provider-neutral host behavior.
// It intentionally does not add a replacement model or provider identity.
func SanitizeThirdPartyInstructions(instructions string) string {
	if instructions == "" {
		return ""
	}
	// The official catalog currently starts with a model-identity paragraph.
	// Remove that paragraph only when it is actually an official Codex/GPT
	// identity. Keep all other host instructions, but do not replace the removed
	// identity with provider-specific or generic identity guidance.
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
	return instructions
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

func cloneValue(value any) any {
	data, _ := json.Marshal(value)
	var out any
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
