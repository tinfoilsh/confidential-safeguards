package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	idlePollInterval = 500 * time.Millisecond
	reportAttempts   = 3
	reportRetryDelay = 5 * time.Second
)

type ingestRequest struct {
	Credential     string    `json:"credential"`
	ConversationID string    `json:"conversation_id"`
	Messages       []Message `json:"messages"`
}

// Classifier gives the first-pass verdict on a transcript.
type Classifier interface {
	Classify(ctx context.Context, transcript string) (*Verdict, error)
}

// Reviewer second-guesses a flag from the Classifier; its verdict is final.
type Reviewer interface {
	Review(ctx context.Context, transcript string, judge *Verdict) (*Verdict, error)
}

// Reporter delivers a confirmed violation outside the enclave.
type Reporter interface {
	ReportViolation(ctx context.Context, v Violation) error
}

type Service struct {
	queue      *Queue
	classifier Classifier
	reviewer   Reviewer
	reporter   Reporter

	maxRequestBytes    int
	maxTranscriptBytes int
	classifyTimeout    time.Duration
	reviewTimeout      time.Duration

	// sleep waits for d or until ctx is done; tests swap it out.
	sleep func(ctx context.Context, d time.Duration)
}

func NewService(cfg *Config, classifier Classifier, reviewer Reviewer, reporter Reporter) *Service {
	return &Service{
		queue:              NewQueue(cfg.QueueTTL, cfg.QueueMaxSize),
		classifier:         classifier,
		reviewer:           reviewer,
		reporter:           reporter,
		maxRequestBytes:    cfg.MaxRequestBytes,
		maxTranscriptBytes: cfg.MaxTranscriptBytes,
		classifyTimeout:    cfg.SafeguardTimeout,
		reviewTimeout:      cfg.SafeguardReviewTimeout,
		sleep:              sleep,
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (s *Service) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.maxRequestBytes))
	dec := json.NewDecoder(r.Body)
	var req ingestRequest
	err := dec.Decode(&req)
	if err == nil {
		if _, err = dec.Token(); err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Credential) == "" {
		http.Error(w, "credential is required", http.StatusBadRequest)
		return
	}
	conv, err := NewConversation(req.Credential, req.ConversationID, req.Messages, s.maxTranscriptBytes)
	if err != nil {
		// Static message: never echo anything conversation-derived back out.
		http.Error(w, "invalid messages", http.StatusBadRequest)
		return
	}

	s.queue.Push(conv)
	w.WriteHeader(http.StatusAccepted)
}

// RunWorker drains the queue until ctx is cancelled.
func (s *Service) RunWorker(ctx context.Context) {
	for ctx.Err() == nil {
		conv := s.queue.Pop()
		if conv == nil {
			s.sleep(ctx, idlePollInterval)
			continue
		}
		s.process(ctx, conv)
	}
}

func (s *Service) process(ctx context.Context, conv *Conversation) {
	classifyCtx, cancel := context.WithTimeout(ctx, s.classifyTimeout)
	defer cancel()
	verdict, err := s.classifier.Classify(classifyCtx, conv.Transcript)
	if err != nil {
		slog.Warn("classification failed; conversation dropped", "error", err)
		return
	}
	if !verdict.Violation {
		return
	}

	reviewCtx, cancelReview := context.WithTimeout(ctx, s.reviewTimeout)
	defer cancelReview()
	review, err := s.reviewer.Review(reviewCtx, conv.Transcript, verdict)
	if err != nil {
		slog.Warn("review failed; flagged conversation dropped", "error", err)
		return
	}
	if !review.Violation {
		return
	}

	violation := Violation{Credential: conv.Credential, ConversationID: conv.ConversationID}
	for attempt := 1; ; attempt++ {
		err := s.reporter.ReportViolation(ctx, violation)
		if err == nil {
			slog.Warn("violation reported")
			return
		}
		if attempt == reportAttempts {
			slog.Error("failed to report violation; giving up", "error", err)
			return
		}
		s.sleep(ctx, reportRetryDelay)
		if ctx.Err() != nil {
			return
		}
	}
}
