package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Message is one turn in the OpenAI chat format.
type Message struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// Content is a message body flattened to text. It decodes from any of the
// OpenAI content forms: null, a string, or an array of typed parts. Non-text
// parts are replaced with a placeholder so binary data never enters the queue.
type Content string

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *Content) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*c = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*c = Content(text)
		return nil
	}
	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return errors.New("message content must be a string or an array of content parts")
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		if p.Type == "text" {
			b.WriteString(p.Text)
		} else {
			fmt.Fprintf(&b, "[%s]", p.Type)
		}
	}
	*c = Content(b.String())
	return nil
}

// Conversation is a chat history reduced to a bounded transcript. Prefixes[i]
// is a chain hash covering the first i+1 messages, salted with the caller's
// credential and conversation id, so a conversation at turn n+1 carries the
// hash of the same conversation at turn n while distinct chats with identical
// openings stay separate. Only the transcript is retained, so memory held per
// queued conversation is bounded by maxTranscriptBytes.
type Conversation struct {
	Credential     string
	ConversationID string
	Prefixes       []string
	Transcript     string
}

func (c *Conversation) Hash() string {
	return c.Prefixes[len(c.Prefixes)-1]
}

func NewConversation(credential, conversationID string, messages []Message, maxTranscriptBytes int) (*Conversation, error) {
	if len(messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	conv := &Conversation{Credential: credential, ConversationID: conversationID}
	prev := chainHash([sha256.Size]byte{}, credential, conversationID)
	for _, m := range messages {
		if m.Role == "" {
			return nil, errors.New("message role is required")
		}
		prev = chainHash(prev, m.Role, string(m.Content))
		conv.Prefixes = append(conv.Prefixes, hex.EncodeToString(prev[:]))
	}
	conv.Transcript = renderTranscript(messages, maxTranscriptBytes)
	return conv, nil
}

// chainHash extends prev with length-prefixed fields, so no two field
// sequences share an encoding.
func chainHash(prev [sha256.Size]byte, fields ...string) [sha256.Size]byte {
	h := sha256.New()
	h.Write(prev[:])
	for _, f := range fields {
		binary.Write(h, binary.BigEndian, uint64(len(f)))
		h.Write([]byte(f))
	}
	return [sha256.Size]byte(h.Sum(nil))
}

const turnSeparator = "\n\n"

// renderTranscript formats the turns for the classifier. When the result would
// exceed maxBytes the oldest turns are dropped first; the final turn is always
// present, truncated from the front if it alone is too long.
func renderTranscript(messages []Message, maxBytes int) string {
	var lines []string
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		line := fmt.Sprintf("[%s]\n%s", messages[i].Role, messages[i].Content)
		if len(lines) > 0 {
			total += len(turnSeparator)
		}
		if total+len(line) > maxBytes {
			if len(lines) == 0 {
				lines = append(lines, tail(line, maxBytes))
			}
			break
		}
		lines = append(lines, line)
		total += len(line)
	}
	slices.Reverse(lines)
	return strings.Join(lines, turnSeparator)
}

// tail returns at most n trailing bytes of s without splitting a UTF-8 sequence.
func tail(s string, n int) string {
	if n >= len(s) {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
