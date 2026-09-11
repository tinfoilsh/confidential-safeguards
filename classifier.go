package main

import (
	"context"

	"github.com/openai/openai-go/v3"
)

const classifyMaxTokens = 8192

type SafeguardClassifier struct {
	client *openai.Client
	model  string
	policy string
}

func NewSafeguardClassifier(client *openai.Client, model, policy string) *SafeguardClassifier {
	return &SafeguardClassifier{client: client, model: model, policy: policy}
}

func (c *SafeguardClassifier) Classify(ctx context.Context, transcript string) (*Verdict, error) {
	return requestVerdict(ctx, c.client, c.model, classifyMaxTokens, c.policy, transcript)
}
