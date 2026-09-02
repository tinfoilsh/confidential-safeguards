package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

const testMaxTranscript = 1 << 20

func mustConversation(t *testing.T, credential string, messages string) *Conversation {
	t.Helper()
	conv, err := NewConversation(credential, "", json.RawMessage(messages), testMaxTranscript)
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
	c, err := NewConversation("u1", "chat-2", json.RawMessage("["+turnOne+"]"), testMaxTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() == b.Hash() || a.Hash() == c.Hash() {
		t.Fatal("same content for different credentials or conversations must hash differently")
	}
}

func TestNewConversation_ContentParts(t *testing.T) {
	conv := mustConversation(t, "u1", `[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"assistant","content":null}]`)
	if want := "[user]\nlook\n[image_url]\n\n[assistant]\n"; conv.Transcript != want {
		t.Fatalf("transcript = %q, want %q", conv.Transcript, want)
	}
}

func TestNewConversation_Rejects(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        `[]`,
		"not array":    `{"role":"user"}`,
		"missing role": `[{"content":"x"}]`,
		"bad content":  `[{"role":"user","content":42}]`,
	} {
		if _, err := NewConversation("u1", "", json.RawMessage(body), testMaxTranscript); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func transcript(t *testing.T, messages string, maxBytes int) string {
	t.Helper()
	conv, err := NewConversation("u1", "", json.RawMessage(messages), maxBytes)
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
