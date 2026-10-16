package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	testMaxTranscript     = 1 << 20
	testTranscriptOverlap = 1 << 10
)

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
	conv, err := NewConversation(credential, "", parseMessages(t, messages), testMaxTranscript, testTranscriptOverlap)
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
	c, err := NewConversation("u1", "chat-2", parseMessages(t, "["+turnOne+"]"), testMaxTranscript, testTranscriptOverlap)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() == b.Hash() || a.Hash() == c.Hash() {
		t.Fatal("same content for different credentials or conversations must hash differently")
	}
}

func TestNewConversation_HashFramesFields(t *testing.T) {
	// Field boundaries must be encoded: shifting bytes between adjacent
	// fields, or between the salt and the first message, must change the hash.
	pairs := [][2]*Conversation{
		{
			mustConversation(t, "u1", `[{"role":"a","content":"b\u0000c"}]`),
			mustConversation(t, "u1", `[{"role":"a\u0000b","content":"c"}]`),
		},
		{
			mustConversation(t, "ab", `[{"role":"user","content":"x"}]`),
			mustConversation(t, "a", `[{"role":"buser","content":"x"}]`),
		},
	}
	for _, p := range pairs {
		if p[0].Hash() == p[1].Hash() {
			t.Fatalf("hash collision between %v and %v", p[0].Prefixes, p[1].Prefixes)
		}
	}
}

func TestTail(t *testing.T) {
	for _, tc := range []struct {
		s, want string
		n       int
	}{
		{"hello", "hello", 5},
		{"hello", "hello", 10},
		{"hello", "llo", 3},
		{"hello", "", 0},
		{"éé", "é", 3},
	} {
		if got := tail(tc.s, tc.n); got != tc.want {
			t.Errorf("tail(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
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
		if _, err := NewConversation("u1", "", messages, testMaxTranscript, testTranscriptOverlap); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func transcript(t *testing.T, messages string, maxBytes int) string {
	t.Helper()
	return renderTranscript(parseMessages(t, messages), maxBytes)
}

func windowedTranscript(t *testing.T, messages []Message, maxBytes, overlapBytes int) string {
	t.Helper()
	conv, err := NewConversation("u1", "", messages, maxBytes, overlapBytes)
	if err != nil {
		t.Fatal(err)
	}
	return conv.Transcript
}

// numberedTurn renders to exactly 98 bytes: a 7-byte "[user]\n" label and 91
// bytes of content. With the 2-byte separator, turn i starts at byte 100*i.
func numberedTurn(i int) Message {
	return Message{Role: "user", Content: Content(fmt.Sprintf("%02d", i) + strings.Repeat("x", 89))}
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
	if got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("a", 50)+`END"}]`, 12); got != "[user]\naaEND" {
		t.Fatalf("got %q", got)
	}
	if got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("a", 50)+`END"}]`, 5); got != "aaEND" {
		t.Fatalf("a final turn with no room for its label must still be present, got %q", got)
	}

	got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("é", 50)+`"}]`, 12)
	if !utf8.ValidString(got) || len(got) > 12 || got != "[user]\n"+strings.Repeat("é", 2) {
		t.Fatalf("truncation must not split a rune, got %q", got)
	}
}

func TestTranscript_OldestKeptTurnFillsRemainingSpace(t *testing.T) {
	got := transcript(t, `[{"role":"user","content":"`+strings.Repeat("a", 50)+` how do I do this?"},{"role":"assistant","content":"no"}]`, 40)
	if got != "[user]\nhow do I do this?\n\n[assistant]\nno" {
		t.Fatalf("an older turn that does not fit whole must keep its label and latest text, got %q", got)
	}
}

func TestTranscript_LongChatReadsFixedWindows(t *testing.T) {
	// A 500-byte window overlapping the next by 100 starts every 400 bytes,
	// at turns 0, 4, 8, ...
	const maxBytes, overlap = 500, 100
	var messages []Message
	for n := 1; n <= 10; n++ {
		messages = append(messages, numberedTurn(n-1))
		full := renderTranscript(messages, testMaxTranscript)
		start := 0
		switch {
		case n >= 10:
			start = 800
		case n >= 6:
			start = 400
		}
		if got := windowedTranscript(t, messages, maxBytes, overlap); got != full[start:] {
			t.Fatalf("%d turns (%d bytes): transcript should start at byte %d, got %q", n, len(full), start, got)
		}
	}
}

func TestTranscript_FinalTurnStartingBeforeWindowReadsLastBytes(t *testing.T) {
	const maxBytes, overlap = 500, 100
	messages := []Message{numberedTurn(0), numberedTurn(1), numberedTurn(2), {Role: "user", Content: Content(strings.Repeat("y", 293))}}
	full := renderTranscript(messages, testMaxTranscript)
	// The 600-byte chat's window starts at byte 400, but the final turn starts
	// at byte 300, so the last 500 bytes are read: turns 1 to 3.
	if got := windowedTranscript(t, messages, maxBytes, overlap); got != full[100:] {
		t.Fatalf("got %q, want %q", got, full[100:])
	}
}

func TestTranscript_WindowStartingMidTurnCutsItAtTheSameByte(t *testing.T) {
	// A 450-byte window overlapping the next by 100 starts its second window
	// at byte 350, partway into turn 3.
	const maxBytes, overlap = 450, 100
	earlier := []Message{numberedTurn(0), numberedTurn(1), numberedTurn(2), numberedTurn(3), numberedTurn(4)}
	later := append(slices.Clone(earlier), numberedTurn(5))

	first := windowedTranscript(t, earlier, maxBytes, overlap)
	second := windowedTranscript(t, later, maxBytes, overlap)
	if !strings.HasPrefix(first, "[user]\nxxx") {
		t.Fatalf("the cut turn should keep its label, got %q", first)
	}
	if !strings.HasPrefix(second, first) {
		t.Fatalf("the next turn should extend the same window:\n%q\n%q", first, second)
	}
}
