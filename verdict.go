package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
)

// Everything the classifier and the reviewer share lives in this file, so the
// two passes can be audited for consistency in one place.
const verdictTemperature = 0.0

const categoryNone = "none"

// "none" is a useful outlet for the second pass when there's no flag. Also the first pass gpt-oss is silly and likes to use this.
var violationCategories = []string{categoryNone, "cbrn", "mass_violence", "child_endangerment", "self_harm", "csam"}

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

// requestVerdict asks model for a Verdict under the shared temperature and
// strict schema; only the prompts and the output budget differ between passes.
func requestVerdict(ctx context.Context, client *openai.Client, model string, maxTokens int64, system, user string) (*Verdict, error) {
	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage(user),
		},
		Temperature: openai.Float(verdictTemperature),
		MaxTokens:   openai.Int(maxTokens),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "verdict",
					Schema: verdictSchema,
					Strict: openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s call failed: %w", model, err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("%s returned no choices", model)
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse %s verdict: %w", model, err)
	}
	return &verdict, nil
}
