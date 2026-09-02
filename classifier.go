package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3"
)

const classifierTemperature = 0.0

var violationCategories = []string{"none", "cbrn", "mass_violence", "child_endangerment", "self_harm", "csam"}

// Verdict is the classifier's structured output. The category sharpens the
// model's judgement but stays inside the enclave; only the boolean is acted on.
type Verdict struct {
	Violation bool   `json:"violation"`
	Category  string `json:"category"`
}

var verdictSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"violation": map[string]any{"type": "boolean"},
		"category":  map[string]any{"type": "string", "enum": violationCategories},
	},
	"required":             []string{"violation", "category"},
	"additionalProperties": false,
}

type Classifier interface {
	Classify(ctx context.Context, transcript string) (*Verdict, error)
}

type SafeguardClassifier struct {
	client *openai.Client
	model  string
	policy string
}

func NewSafeguardClassifier(client *openai.Client, model, policy string) *SafeguardClassifier {
	return &SafeguardClassifier{client: client, model: model, policy: policy}
}

func (c *SafeguardClassifier) Classify(ctx context.Context, transcript string) (*Verdict, error) {
	resp, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: c.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(c.policy),
			openai.UserMessage(transcript),
		},
		Temperature: openai.Float(classifierTemperature),
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
		return nil, fmt.Errorf("safeguard call failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("safeguard returned no choices")
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse safeguard response: %w", err)
	}
	return &verdict, nil
}
