package main

// Everything the classifier and the reviewer share lives in this file, so the
// two passes can be audited for consistency in one place.
const verdictTemperature = 0.0

// "none" is a no-op, the safeguard model sometimes emits it, included here for type completeness
var violationCategories = []string{"none", "cbrn", "mass_violence", "child_endangerment", "self_harm", "csam"}

// Verdict is a classification outcome, produced by the classifier and again by
// the reviewer. The categories and reason sharpen the model's judgement and
// are what the reviewer second-guesses, but they don't leave the system of
// enclaves.
type Verdict struct {
	Violation  bool     `json:"violation"`
	Categories []string `json:"categories"`
	Reason     string   `json:"reason"`
}

// verdictSchema is the strict structured-output schema both passes are decoded
// under.
var verdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"violation": map[string]any{"type": "boolean"},
		"categories": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string", "enum": violationCategories},
		},
		"reason": map[string]any{"type": "string"},
	},
	"required":             []string{"violation", "categories", "reason"},
	"additionalProperties": false,
}
