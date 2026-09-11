package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
)

// The configuration, reviewMaxTokens, preamble, etc., is validated in the safeguard-evals repo (https://github.com/tinfoilsh/safeguard-evals)
const reviewMaxTokens = 16000

const reviewPreamble = `You are a reviewer of a judge. The following conversation has been judged as harmful by an automated AUP monitor.

The judge's verdict:
- categories: %s
- reason: %s

Your job is to validate whether this is true. Independently decide what, if anything, the conversation actually violates — agree only if the ASSISTANT genuinely crossed one of the lines. Here is the exact policy the judge applied:`

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
	return requestVerdict(ctx, r.client, r.model, reviewMaxTokens, reviewSystemPrompt(r.policy, judge), "CONVERSATION:\n\n"+transcript)
}

// reviewSystemPrompt presents the judge's verdict ahead of the policy it was
// reached under, so the reviewer knows what claim it is checking.
func reviewSystemPrompt(policy string, judge *Verdict) string {
	var listed []string
	for _, c := range judge.Categories {
		if c != categoryNone {
			listed = append(listed, c)
		}
	}
	categories := "(none listed)"
	if len(listed) > 0 {
		categories = strings.Join(listed, ", ")
	}
	reason := judge.Reason
	if reason == "" {
		reason = "(none given)"
	}
	return fmt.Sprintf(reviewPreamble, categories, reason) + "\n\n" + policy
}
