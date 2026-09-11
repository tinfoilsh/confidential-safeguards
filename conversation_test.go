package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

const testMaxTranscript = 1 << 20

const (
	turnOne = `{"role":"system","content":"be helpful"},{"role":"user","content":"hello"},{"role":"assistant","content":"Hello, how can I help you?"}`
	turnTwo = turnOne + `,{"role":"user","content":"what is 2+2?"},{"role":"assistant","content":"4"}`
)

func parseMessages(t *testing.T, messages string) []Message {
	t.Helper()
	var parsed []Message
	if err := json.Unmarshal([]byte(messages), &parsed); err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	return parsed
}

func mustConversation(t *testing.T, credential string, messages string) *Conversation {
	t.Helper()
	conv, err := NewConversation(credential, "", parseMessages(t, messages), testMaxTranscript)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	return conv
}

func TestNewConversation_PrefixChainExtends(t *testing.T) {
	first := mustConversation(t, "u1", "["+turnOne+"]")
	second := mustConversation(t, "u1", "["+turnTwo+"]")

	if len(first.Prefixes) != 3 || len(second.Prefixes) != 5 {
		t.Fatalf("prefix counts = %d, %d", len(first.Prefixes), len(second.Prefixes))
	}
	if second.Prefixes[2] != first.Hash() {
		t.Fatal("turn-two conversation should contain the turn-one hash as a prefix")
	}
	if second.Hash() == first.Hash() {
		t.Fatal("distinct conversations must have distinct hashes")
	}
}

func TestNewConversation_SaltedByCredentialAndConversation(t *testing.T) {
	a := mustConversation(t, "u1", "["+turnOne+"]")
	b := mustConversation(t, "u2", "["+turnOne+"]")
	c, err := NewConversation("u1", "chat-2", parseMessages(t, "["+turnOne+"]"), testMaxTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() == b.Hash() || a.Hash() == c.Hash() {
		t.Fatal("same content for different credentials or conversations must hash differently")
	}
}

func TestContent_DecodesOpenAIForms(t *testing.T) {
	for raw, want := range map[string]Content{
		`null`:    "",
		`"plain"`: "plain",
		`[]`:      "",
		`[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]`: "look\n[image_url]",
	} {
		var got Content
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Errorf("%s: %v", raw, err)
		} else if got != want {
			t.Errorf("%s: content = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{`42`, `true`, `{"text":"x"}`, `[1]`} {
		var got Content
		if err := json.Unmarshal([]byte(raw), &got); err == nil {
			t.Errorf("%s: expected error, got %q", raw, got)
		}
	}
}

func TestNewConversation_ContentParts(t *testing.T) {
	conv := mustConversation(t, "u1", `[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","content":null}]`)
	if want := "[user]\nlook\n[image_url]\n\n[assistant]\n"; conv.Transcript != want {
		t.Fatalf("transcript = %q, want %q", conv.Transcript, want)
	}
}

func TestNewConversation_Rejects(t *testing.T) {
	for name, messages := range map[string][]Message{
		"empty":        {},
		"missing role": {{Content: "x"}},
	} {
		if _, err := NewConversation("u1", "", messages, testMaxTranscript); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func transcript(t *testing.T, messages string, maxBytes int) string {
	t.Helper()
	conv, err := NewConversation("u1", "", parseMessages(t, messages), maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return conv.Transcript
}

func TestTranscript_DropsOldestTurnsFirst(t *testing.T) {
	full := transcript(t, "["+turnTwo+"]", testMaxTranscript)
	if !strings.HasPrefix(full, "[system]\nbe helpful") || !strings.HasSuffix(full, "[assistant]\n4") {
		t.Fatalf("unexpected full transcript:\n%s", full)
	}

	for _, limit := range []int{40, 41, 42, 43, 60} {
		short := transcript(t, "["+turnTwo+"]", limit)
		if strings.Contains(short, "be helpful") || !strings.HasSuffix(short, "[assistant]\n4") {
			t.Fatalf("truncated transcript should drop the oldest turns:\n%s", short)
		}
		if len(short) > limit {
			t.Fatalf("transcript length %d exceeds limit %d", len(short), limit)
		}
	}
}

func TestTranscript_LastTurnTruncatedFromFront(t *testing.T) {
	if got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("a", 50)+`END"}]`, 10); got != "aaaaaaaEND" {
		t.Fatalf("got %q", got)
	}

	got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("é", 50)+`"}]`, 11)
	if !utf8.ValidString(got) || len(got) > 11 || got != strings.Repeat("é", 5) {
		t.Fatalf("truncation must not split a rune, got %q", got)
	}
}
