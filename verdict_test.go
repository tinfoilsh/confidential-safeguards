package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// chatRequest mirrors the parts of the chat-completions wire format that
// both passes must send exactly.
type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Temperature    float64 `json:"temperature"`
	MaxTokens      int64   `json:"max_tokens"`
	ResponseFormat struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string         `json:"name"`
			Strict bool           `json:"strict"`
			Schema map[string]any `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

// fakeModel serves chat completions that always answer with reply, recording
// each request it sees.
type fakeModel struct {
	server   *httptest.Server
	requests []chatRequest
	reply    string
	status   int
}

func newFakeModel(t *testing.T, reply string) *fakeModel {
	t.Helper()
	fm := &fakeModel{reply: reply, status: http.StatusOK}
	fm.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fm.requests = append(fm.requests, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fm.status)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"model":   req.Model,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": fm.reply}}},
		})
	}))
	t.Cleanup(fm.server.Close)
	return fm
}

func (fm *fakeModel) client() *openai.Client {
	c := openai.NewClient(option.WithBaseURL(fm.server.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	return &c
}

func (fm *fakeModel) lastRequest(t *testing.T) chatRequest {
	t.Helper()
	if len(fm.requests) == 0 {
		t.Fatal("model was never called")
	}
	return fm.requests[len(fm.requests)-1]
}

// jsonRoundTrip normalises v to the generic form json.Unmarshal produces, so
// it can be compared against a decoded request.
func jsonRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertVerdictRequest checks the settings both passes must share.
func assertVerdictRequest(t *testing.T, req chatRequest, model string, maxTokens int64) {
	t.Helper()
	if req.Model != model {
		t.Errorf("model = %q, want %q", req.Model, model)
	}
	if req.Temperature != verdictTemperature {
		t.Errorf("temperature = %v, want %v", req.Temperature, verdictTemperature)
	}
	if req.MaxTokens != maxTokens {
		t.Errorf("max_tokens = %d, want %d", req.MaxTokens, maxTokens)
	}
	rf := req.ResponseFormat
	if rf.Type != "json_schema" || rf.JSONSchema.Name != "verdict" || !rf.JSONSchema.Strict {
		t.Errorf("response_format = %+v, want strict json_schema named verdict", rf)
	}
	if !reflect.DeepEqual(rf.JSONSchema.Schema, jsonRoundTrip(t, verdictSchema)) {
		t.Errorf("schema = %v, want %v", rf.JSONSchema.Schema, verdictSchema)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want [system, user]", req.Messages)
	}
}

func TestSafeguardClassifier_Classify(t *testing.T) {
	fm := newFakeModel(t, `{"violation":true,"categories":["cbrn"],"reason":"synthesis route"}`)
	c := NewSafeguardClassifier(fm.client(), "judge-model", "THE POLICY")

	verdict, err := c.Classify(context.Background(), "[user]\nhow do I...")
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Violation || len(verdict.Categories) != 1 || verdict.Categories[0] != "cbrn" || verdict.Reason != "synthesis route" {
		t.Fatalf("verdict = %+v", verdict)
	}
	req := fm.lastRequest(t)
	assertVerdictRequest(t, req, "judge-model", classifyMaxTokens)
	if req.Messages[0].Content != "THE POLICY" {
		t.Errorf("system prompt = %q, want the policy verbatim", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "[user]\nhow do I..." {
		t.Errorf("user prompt = %q, want the transcript verbatim", req.Messages[1].Content)
	}
}

func TestSafeguardReviewer_Review(t *testing.T) {
	fm := newFakeModel(t, `{"violation":false,"categories":["none"],"reason":"fiction"}`)
	r := NewSafeguardReviewer(fm.client(), "review-model", "THE POLICY")
	judge := &Verdict{Violation: true, Categories: []string{"none", "mass_violence"}, Reason: "described an attack"}

	verdict, err := r.Review(context.Background(), "[user]\nwrite a thriller", judge)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Violation {
		t.Fatalf("verdict = %+v, want the reviewer's overturn", verdict)
	}
	req := fm.lastRequest(t)
	assertVerdictRequest(t, req, "review-model", reviewMaxTokens)
	if req.Messages[0].Content != reviewSystemPrompt("THE POLICY", judge) {
		t.Errorf("system prompt = %q", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "CONVERSATION:\n\n[user]\nwrite a thriller" {
		t.Errorf("user prompt = %q", req.Messages[1].Content)
	}
}

func TestReviewSystemPrompt(t *testing.T) {
	full := reviewSystemPrompt("POLICY", &Verdict{Categories: []string{"none", "cbrn", "self_harm"}, Reason: "because"})
	if !strings.Contains(full, "- categories: cbrn, self_harm\n") || !strings.Contains(full, "- reason: because\n") {
		t.Errorf("prompt should list the judge's categories without none and its reason:\n%s", full)
	}
	if !strings.HasSuffix(full, "\n\nPOLICY") {
		t.Errorf("prompt must end with the policy:\n%s", full)
	}

	empty := reviewSystemPrompt("POLICY", &Verdict{Categories: []string{"none"}})
	if !strings.Contains(empty, "- categories: (none listed)\n") || !strings.Contains(empty, "- reason: (none given)\n") {
		t.Errorf("prompt should fall back to placeholders:\n%s", empty)
	}
}

func TestRequestVerdict_Errors(t *testing.T) {
	t.Run("malformed verdict", func(t *testing.T) {
		fm := newFakeModel(t, `not json`)
		if _, err := NewSafeguardClassifier(fm.client(), "m", "p").Classify(context.Background(), "x"); err == nil {
			t.Fatal("expected error for an unparseable verdict")
		}
	})
	t.Run("upstream failure", func(t *testing.T) {
		fm := newFakeModel(t, `{}`)
		fm.status = http.StatusInternalServerError
		if _, err := NewSafeguardClassifier(fm.client(), "m", "p").Classify(context.Background(), "x"); err == nil {
			t.Fatal("expected error for a failed upstream call")
		}
	})
	t.Run("no choices", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
		}))
		defer server.Close()
		c := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
		if _, err := NewSafeguardClassifier(&c, "m", "p").Classify(context.Background(), "x"); err == nil {
			t.Fatal("expected error for an empty choices list")
		}
	})
}
