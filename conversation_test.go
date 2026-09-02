package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func mustConversation(t *testing.T, credential string, messages string) *Conversation {
	t.Helper()
	conv, err := NewConversation(credential, "", json.RawMessage(messages))
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	return conv
}

const (
	turnOne = `{"role":"system","content":"be helpful"},{"role":"user","content":"hello"},{"role":"assistant","content":"Hello, how can I help you?"}`
	turnTwo = turnOne + `,{"role":"user","content":"what is 2+2?"},{"role":"assistant","content":"4"}`
)

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
	c, err := NewConversation("u1", "chat-2", json.RawMessage("["+turnOne+"]"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() == b.Hash() || a.Hash() == c.Hash() {
		t.Fatal("same content for different credentials or conversations must hash differently")
	}
}

func TestNewConversation_ContentParts(t *testing.T) {
	conv := mustConversation(t, "u1", `[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","content":null}]`)
	if got := conv.Turns[0].Content; got != "look\n[image_url]" {
		t.Fatalf("content = %q", got)
	}
	if conv.Turns[1].Content != "" {
		t.Fatalf("null content should be empty, got %q", conv.Turns[1].Content)
	}
}

func TestNewConversation_Rejects(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        `[]`,
		"not array":    `{"role":"user"}`,
		"missing role": `[{"content":"x"}]`,
		"bad content":  `[{"role":"user","content":42}]`,
	} {
		if _, err := NewConversation("u1", "", json.RawMessage(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestTranscript_DropsOldestTurnsFirst(t *testing.T) {
	conv := mustConversation(t, "u1", "["+turnTwo+"]")
	full := conv.Transcript(1 << 20)
	if !strings.HasPrefix(full, "[system]\nbe helpful") || !strings.HasSuffix(full, "[assistant]\n4") {
		t.Fatalf("unexpected full transcript:\n%s", full)
	}

	for _, limit := range []int{40, 41, 42, 43, 60} {
		short := conv.Transcript(limit)
		if strings.Contains(short, "be helpful") || !strings.HasSuffix(short, "[assistant]\n4") {
			t.Fatalf("truncated transcript should drop the oldest turns:\n%s", short)
		}
		if len(short) > limit {
			t.Fatalf("transcript length %d exceeds limit %d", len(short), limit)
		}
	}
}

func TestTranscript_LastTurnTruncatedFromFront(t *testing.T) {
	conv := mustConversation(t, "u1", `[{"role":"user","content":"`+strings.Repeat("a", 50)+`END"}]`)
	if got := conv.Transcript(10); got != "aaaaaaaEND" {
		t.Fatalf("got %q", got)
	}

	conv = mustConversation(t, "u1", `[{"role":"user","content":"`+strings.Repeat("é", 50)+`"}]`)
	got := conv.Transcript(11)
	if !utf8.ValidString(got) || len(got) > 11 || got != strings.Repeat("é", 5) {
		t.Fatalf("truncation must not split a rune, got %q", got)
	}
}
