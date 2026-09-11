package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/tinfoilsh/confidential-safeguards/config"
)

const (
	idlePollInterval = 500 * time.Millisecond
	reportAttempts   = 3
	reportRetryDelay = 5 * time.Second
)

type ingestRequest struct {
	Credential     string          `json:"credential"`
	ConversationID string          `json:"conversation_id"`
	Messages       json.RawMessage `json:"messages"`
}

type Service struct {
	queue      *Queue
	classifier Classifier
	reviewer   Reviewer
	notifier   Notifier

	maxRequestBytes    int64
	maxTranscriptBytes int
	classifyTimeout    time.Duration
	reportRetryDelay   time.Duration
}

func NewService(cfg *config.Config, classifier Classifier, reviewer Reviewer, notifier Notifier) *Service {
	return &Service{
		queue:              NewQueue(cfg.QueueTTL, cfg.QueueMaxSize),
		classifier:         classifier,
		reviewer:           reviewer,
		notifier:           notifier,
		maxRequestBytes:    cfg.MaxRequestBytes,
		maxTranscriptBytes: cfg.MaxTranscriptBytes,
		classifyTimeout:    cfg.SafeguardTimeout,
		reportRetryDelay:   reportRetryDelay,
	}
}

func (s *Service) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestBytes)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
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
			select {
			case <-ctx.Done():
				return
			case <-time.After(idlePollInterval):
			}
			continue
		}
		s.process(ctx, conv)
	}
}

func (s *Service) process(ctx context.Context, conv *Conversation) {
	logger := log.WithField("turns", len(conv.Prefixes))

	classifyCtx, cancel := context.WithTimeout(ctx, s.classifyTimeout)
	defer cancel()
	verdict, err := s.classifier.Classify(classifyCtx, conv.Transcript)
	if err != nil {
		logger.WithError(err).Warn("classification failed; conversation dropped")
		return
	}
	if !verdict.Violation {
		return
	}

	reviewCtx, cancelReview := context.WithTimeout(ctx, s.classifyTimeout)
	defer cancelReview()
	review, err := s.reviewer.Review(reviewCtx, conv.Transcript, verdict)
	if err != nil {
		logger.WithError(err).Warn("review failed; conversation dropped")
		return
	}
	if !review.Violation {
		logger.Info("classifier flag overturned by reviewer")
		return
	}

	violation := Violation{Credential: conv.Credential, ConversationID: conv.ConversationID}
	for attempt := 1; ; attempt++ {
		err = s.notifier.ReportViolation(ctx, violation)
		if err == nil {
			logger.Warn("violation reported")
			return
		}
		if attempt == reportAttempts || ctx.Err() != nil {
			logger.WithError(err).Error("failed to report violation; giving up")
			return
		}
		logger.WithError(err).Warn("failed to report violation; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.reportRetryDelay):
		}
	}
}
