// The configuration, reviewMaxTokens, preamble, etc., is validated in the safeguards-eval repo (https://github.com/tinfoilsh/safeguard-evals)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
)

const reviewMaxTokens = 16000

const reviewPreamble = `You are a reviewer of a judge. The following conversation has been judged as harmful by an automated AUP monitor.

The judge's verdict:
- categories: %s
- reason: %s

Your job is to validate whether this is true. Independently decide what, if anything, the conversation actually violates — agree only if the ASSISTANT genuinely crossed one of the lines. Here is the exact policy the judge applied:`

type Reviewer interface {
	Review(ctx context.Context, transcript string, judge *Verdict) (*Verdict, error)
}

// Like the classifier's, SafeguardReviewer's verdict stays inside the enclave.
type SafeguardReviewer struct {
	client *openai.Client
	model  string
	policy string
}

func NewSafeguardReviewer(client *openai.Client, model, policy string) *SafeguardReviewer {
	return &SafeguardReviewer{client: client, model: model, policy: policy}
}

func (r *SafeguardReviewer) Review(ctx context.Context, transcript string, judge *Verdict) (*Verdict, error) {
	categories := "(none listed)"
	if len(judge.Categories) > 0 {
		categories = strings.Join(judge.Categories, ", ")
	}
	reason := judge.Reason
	if reason == "" {
		reason = "(none given)"
	}
	system := fmt.Sprintf(reviewPreamble, categories, reason) + "\n\n" + r.policy

	resp, err := r.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: r.model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(system),
			openai.UserMessage("CONVERSATION:\n\n" + transcript),
		},
		Temperature: openai.Float(classifierTemperature),
		MaxTokens:   openai.Int(reviewMaxTokens),
	})
	if err != nil {
		return nil, fmt.Errorf("review call failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("reviewer returned no choices")
	}
	return parseVerdict(resp.Choices[0].Message.Content)
}

var verdictJSON = regexp.MustCompile(`(?s)\{.*\}`)

// parseVerdict pulls the verdict JSON out of a reply leniently: the reviewer
// model reasons inline, so the verdict may be surrounded by free text.
func parseVerdict(text string) (*Verdict, error) {
	match := verdictJSON.FindString(text)
	if match == "" {
		return nil, errors.New("no verdict JSON in reviewer reply")
	}
	var verdict Verdict
	if err := json.Unmarshal([]byte(match), &verdict); err != nil {
		return nil, fmt.Errorf("failed to parse reviewer verdict: %w", err)
	}
	return &verdict, nil
}
