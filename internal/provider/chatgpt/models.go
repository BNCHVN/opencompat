package chatgpt

// ModelConfig contains configuration for a specific model.
type ModelConfig struct {
	PromptFile    string
	SupportsNone  bool // Can reasoning be disabled?
	SupportsXHigh bool // Supports "xhigh" reasoning effort?
	DefaultEffort string
	MinEffort     string // Minimum allowed effort
}

// modelConfigs maps model IDs to their configurations.
var modelConfigs = map[string]ModelConfig{
	"gpt-5.2-codex": {
		PromptFile:    "gpt-5.2-codex_prompt.md",
		SupportsNone:  false,
		SupportsXHigh: true,
		DefaultEffort: "medium",
		MinEffort:     "low",
	},
	"gpt-5.1-codex-max": {
		PromptFile:    "gpt-5.1-codex-max_prompt.md",
		SupportsNone:  false,
		SupportsXHigh: true,
		DefaultEffort: "high",
		MinEffort:     "low",
	},
	"gpt-5.1-codex": {
		PromptFile:    "gpt_5_codex_prompt.md",
		SupportsNone:  false,
		SupportsXHigh: false,
		DefaultEffort: "medium",
		MinEffort:     "low",
	},
	"gpt-5-codex": {
		PromptFile:    "gpt_5_codex_prompt.md",
		SupportsNone:  false,
		SupportsXHigh: false,
		DefaultEffort: "medium",
		MinEffort:     "low",
	},
	"gpt-5.1-codex-mini": {
		PromptFile:    "gpt_5_codex_prompt.md",
		SupportsNone:  false,
		SupportsXHigh: false,
		DefaultEffort: "medium",
		MinEffort:     "medium", // Only medium or high
	},
	"gpt-5.2": {
		PromptFile:    "gpt_5_2_prompt.md",
		SupportsNone:  true,
		SupportsXHigh: true,
		DefaultEffort: "medium",
		MinEffort:     "none",
	},
	"gpt-5.1": {
		PromptFile:    "gpt_5_1_prompt.md",
		SupportsNone:  true,
		SupportsXHigh: false,
		DefaultEffort: "medium",
		MinEffort:     "none",
	},
	"gpt-5": {
		PromptFile:    "gpt_5_1_prompt.md",
		SupportsNone:  true,
		SupportsXHigh: false,
		DefaultEffort: "medium",
		MinEffort:     "none",
	},
}

// GetPromptFile returns the prompt file name for a model.
func GetPromptFile(modelID string) string {
	if cfg, ok := modelConfigs[modelID]; ok {
		return cfg.PromptFile
	}
	// Default fallback
	return "gpt_5_codex_prompt.md"
}

// NormalizeReasoningEffort adjusts the reasoning effort based on model capabilities.
func NormalizeReasoningEffort(modelID, effort string) string {
	cfg, ok := modelConfigs[modelID]
	if !ok {
		return effort
	}

	// Effort levels in order
	effortLevels := []string{"none", "low", "medium", "high", "xhigh"}
	effortIndex := map[string]int{
		"none":   0,
		"low":    1,
		"medium": 2,
		"high":   3,
		"xhigh":  4,
	}

	minIdx, minOk := effortIndex[cfg.MinEffort]
	reqIdx, reqOk := effortIndex[effort]

	if !reqOk {
		// Invalid effort, use default
		return cfg.DefaultEffort
	}

	if !minOk {
		minIdx = 0
	}

	// Clamp to minimum
	if reqIdx < minIdx {
		effort = effortLevels[minIdx]
	}

	// Check "none" support
	if effort == "none" && !cfg.SupportsNone {
		effort = "low"
	}

	// Check "xhigh" support
	if effort == "xhigh" && !cfg.SupportsXHigh {
		effort = "high"
	}

	return effort
}

// modelAliases maps user-friendly model names to API model names.
var modelAliases = map[string]string{
	// Codex models
	"codex":             "gpt-5.1-codex",
	"codex-mini":        "gpt-5.1-codex-mini",
	"codex-mini-latest": "gpt-5.1-codex-mini",
	"codex-max":         "gpt-5.1-codex-max",

	// GPT-5 series aliases
	"gpt-5":       "gpt-5.1",
	"gpt-5-codex": "gpt-5.1-codex",

	// Latest aliases (point to most recent version)
	"gpt-5-latest":             "gpt-5.2",
	"gpt-5.2-latest":           "gpt-5.2",
	"gpt-5.1-latest":           "gpt-5.1",
	"gpt-5-codex-latest":       "gpt-5.2-codex",
	"gpt-5.2-codex-latest":     "gpt-5.2-codex",
	"gpt-5.1-codex-latest":     "gpt-5.1-codex",
	"codex-latest":             "gpt-5.2-codex",
	"gpt-5.1-codex-max-latest": "gpt-5.1-codex-max",
}

// effortSuffixes are valid reasoning effort suffixes for model names.
var effortSuffixes = map[string]bool{
	"none":   true,
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
}

// ParseModelWithEffort parses a model name that may include an effort suffix.
// For example, "gpt-5-high" returns ("gpt-5", "high").
// If no effort suffix is found, returns the original model and empty string.
func ParseModelWithEffort(model string) (baseModel string, effort string) {
	// Try to find effort suffix
	for suffix := range effortSuffixes {
		suffixWithDash := "-" + suffix
		if len(model) > len(suffixWithDash) && model[len(model)-len(suffixWithDash):] == suffixWithDash {
			return model[:len(model)-len(suffixWithDash)], suffix
		}
	}
	return model, ""
}

// NormalizeModelNameWithEffort normalizes a model name and extracts any effort suffix.
// Returns the canonical model name and the extracted effort (empty if none).
func NormalizeModelNameWithEffort(model string) (normalizedModel string, effort string) {
	// Strip provider prefix
	if idx := lastIndexByte(model, '/'); idx != -1 {
		model = model[idx+1:]
	}

	// Parse effort suffix
	baseModel, effort := ParseModelWithEffort(model)

	// Try alias lookup on base model
	if canonical, ok := modelAliases[baseModel]; ok {
		return canonical, effort
	}

	// Also try alias on full model (for aliases that include effort)
	if canonical, ok := modelAliases[model]; ok {
		return canonical, ""
	}

	// If we found an effort suffix, return base with effort
	if effort != "" {
		return baseModel, effort
	}

	return model, ""
}

// lastIndexByte returns the index of the last instance of c in s, or -1 if c is not present.
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// GetAllPromptFiles returns a deduplicated list of all prompt files used by models.
func GetAllPromptFiles() []string {
	seen := make(map[string]bool)
	var files []string

	for _, cfg := range modelConfigs {
		if !seen[cfg.PromptFile] {
			seen[cfg.PromptFile] = true
			files = append(files, cfg.PromptFile)
		}
	}

	return files
}
