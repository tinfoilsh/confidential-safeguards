package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Turn struct {
	Role    string
	Content string
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

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewConversation(credential, conversationID string, messages json.RawMessage, maxTranscriptBytes int) (*Conversation, error) {
	var raw []rawMessage
	if err := json.Unmarshal(messages, &raw); err != nil {
		return nil, fmt.Errorf("messages must be an array of chat messages")
	}
	if len(raw) == 0 {
		return nil, errors.New("messages must not be empty")
	}

	conv := &Conversation{Credential: credential, ConversationID: conversationID}
	turns := make([]Turn, 0, len(raw))
	prev := sha256.Sum256([]byte(credential + "\x00" + conversationID))
	for _, m := range raw {
		if m.Role == "" {
			return nil, errors.New("message role is required")
		}
		content, err := parseContent(m.Content)
		if err != nil {
			return nil, err
		}
		turns = append(turns, Turn{Role: m.Role, Content: content})

		h := sha256.New()
		h.Write(prev[:])
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(content))
		prev = [sha256.Size]byte(h.Sum(nil))
		conv.Prefixes = append(conv.Prefixes, hex.EncodeToString(prev[:]))
	}
	conv.Transcript = renderTranscript(turns, maxTranscriptBytes)
	return conv, nil
}

// parseContent accepts the OpenAI content forms: null, a string, or an array
// of typed parts. Non-text parts are replaced with a placeholder so binary
// data never enters the queue.
func parseContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []rawContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("message content must be a string or an array of content parts")
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
	return b.String(), nil
}

const turnSeparator = "\n\n"

// renderTranscript formats the turns for the classifier. When the result would
// exceed maxBytes the oldest turns are dropped first; the final turn is always
// present, truncated from the front if it alone is too long.
func renderTranscript(turns []Turn, maxBytes int) string {
	var lines []string
	total := 0
	for i := len(turns) - 1; i >= 0; i-- {
		line := fmt.Sprintf("[%s]\n%s", turns[i].Role, turns[i].Content)
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
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, turnSeparator)
}

// tail returns at most n trailing bytes of s without splitting a UTF-8 sequence.
func tail(s string, n int) string {
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
