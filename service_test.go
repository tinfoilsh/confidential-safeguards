package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubClassifier struct {
	verdict Verdict
	err     error
	ctxs    []context.Context
}

func (s *stubClassifier) Classify(ctx context.Context, _ string) (*Verdict, error) {
	s.ctxs = append(s.ctxs, ctx)
	if s.err != nil {
		return nil, s.err
	}
	return &s.verdict, nil
}

type reviewCall struct {
	ctx        context.Context
	transcript string
	judge      Verdict
}

type stubReviewer struct {
	verdict Verdict
	err     error
	calls   []reviewCall
}

func (s *stubReviewer) Review(ctx context.Context, transcript string, judge *Verdict) (*Verdict, error) {
	s.calls = append(s.calls, reviewCall{ctx: ctx, transcript: transcript, judge: *judge})
	if s.err != nil {
		return nil, s.err
	}
	return &s.verdict, nil
}

func confirmingReviewer() *stubReviewer {
	return &stubReviewer{verdict: Verdict{Violation: true}}
}

// stubReporter fails the first `failures` calls and records the rest.
type stubReporter struct {
	mu       sync.Mutex
	reported []Violation
	failures int
	calls    int
}

func (s *stubReporter) ReportViolation(_ context.Context, v Violation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failures {
		return errors.New("control plane unavailable")
	}
	s.reported = append(s.reported, v)
	return nil
}

func (s *stubReporter) snapshot() (calls int, reported []Violation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([]Violation(nil), s.reported...)
}

func testConfig() *Config {
	return &Config{
		MaxRequestBytes:        1 << 20,
		MaxTranscriptBytes:     1 << 20,
		SafeguardTimeout:       time.Second,
		SafeguardReviewTimeout: time.Second,
		QueueTTL:               time.Hour,
		QueueMaxSize:           100,
	}
}

func flaggingService(reporter *stubReporter) *Service {
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, confirmingReviewer(), reporter)
	svc.sleep = func(context.Context, time.Duration) {}
	return svc
}

func ingest(svc *Service, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	svc.HandleIngest(rec, req)
	return rec
}

func TestHandleIngest_Validation(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	for name, body := range map[string]string{
		"invalid json":      `{`,
		"missing cred":      `{"messages":[` + turnOne + `]}`,
		"blank cred":        `{"credential":"  ","messages":[` + turnOne + `]}`,
		"missing turns":     `{"credential":"u1"}`,
		"empty turns":       `{"credential":"u1","messages":[]}`,
		"malformed turns":   `{"credential":"u1","messages":[{"content":"x"}]}`,
		"malformed content": `{"credential":"u1","messages":[{"role":"user","content":42}]}`,
	} {
		if rec := ingest(svc, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	if svc.queue.Len() != 0 {
		t.Fatal("invalid requests must not be queued")
	}
}

func TestHandleIngest_RejectsTrailingData(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	for _, trailing := range []string{" {}", "]", "}", " x"} {
		if rec := ingest(svc, `{"credential":"u1","messages":[`+turnOne+`]}`+trailing); rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, want 400", trailing, rec.Code)
		}
	}
	if svc.queue.Len() != 0 {
		t.Fatal("malformed requests must not be queued")
	}
}

func TestHandleIngest_RejectsNonPost(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	rec := httptest.NewRecorder()
	svc.HandleIngest(rec, httptest.NewRequest(http.MethodGet, "/ingest", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandleIngest_TooLarge(t *testing.T) {
	cfg := testConfig()
	cfg.MaxRequestBytes = 64
	svc := NewService(cfg, &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	if rec := ingest(svc, `{"credential":"u1","messages":[`+turnTwo+`]}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHandleIngest_Queues(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	if rec := ingest(svc, `{"credential":"u1","messages":[`+turnOne+`]}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	ingest(svc, `{"credential":"u1","messages":[`+turnTwo+`]}`)
	if svc.queue.Len() != 1 {
		t.Fatalf("queue len = %d, want 1 after prefix replacement", svc.queue.Len())
	}
}

func TestHandleIngest_TranscriptIsCapped(t *testing.T) {
	cfg := testConfig()
	cfg.MaxTranscriptBytes = 20
	svc := NewService(cfg, &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	ingest(svc, `{"credential":"u1","messages":[`+turnTwo+`]}`)
	conv := svc.queue.Pop()
	if conv == nil || len(conv.Transcript) > cfg.MaxTranscriptBytes {
		t.Fatalf("queued transcript must respect the cap, got %+v", conv)
	}
}

func TestProcess_ReportsViolation(t *testing.T) {
	reporter := &stubReporter{}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true, Categories: []string{"cbrn"}}}, confirmingReviewer(), reporter)
	conv, err := NewConversation("cred-1", "chat-42", parseMessages(t, "["+turnTwo+"]"), testMaxTranscript)
	if err != nil {
		t.Fatal(err)
	}
	svc.process(context.Background(), conv)

	want := Violation{Credential: "cred-1", ConversationID: "chat-42"}
	if len(reporter.reported) != 1 || reporter.reported[0] != want {
		t.Fatalf("reported = %+v, want [%+v]", reporter.reported, want)
	}
}

func TestProcess_SafeConversationNotReported(t *testing.T) {
	reporter := &stubReporter{}
	reviewer := confirmingReviewer()
	svc := NewService(testConfig(), &stubClassifier{}, reviewer, reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 0 {
		t.Fatal("safe conversations must not be reported")
	}
	if len(reviewer.calls) != 0 {
		t.Fatal("reviewer must not run on clean conversations")
	}
}

func TestProcess_ClassifierErrorDropsConversation(t *testing.T) {
	reporter := &stubReporter{}
	reviewer := confirmingReviewer()
	svc := NewService(testConfig(), &stubClassifier{err: errors.New("upstream down")}, reviewer, reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 0 || len(reviewer.calls) != 0 {
		t.Fatal("failed classification must be dropped without review or report")
	}
}

func TestProcess_ReviewerSeesJudgeVerdictAndExactTranscript(t *testing.T) {
	judge := Verdict{Violation: true, Categories: []string{"self_harm"}, Reason: "encouraged self-harm"}
	reviewer := confirmingReviewer()
	svc := NewService(testConfig(), &stubClassifier{verdict: judge}, reviewer, &stubReporter{})
	conv := mustConversation(t, "u1", "["+turnTwo+"]")
	svc.process(context.Background(), conv)
	if len(reviewer.calls) != 1 {
		t.Fatalf("reviewer calls = %d, want 1", len(reviewer.calls))
	}
	call := reviewer.calls[0]
	if call.judge.Reason != judge.Reason || len(call.judge.Categories) != 1 || call.judge.Categories[0] != "self_harm" {
		t.Fatalf("reviewer saw %+v, want %+v", call.judge, judge)
	}
	if call.transcript != conv.Transcript {
		t.Fatalf("reviewer transcript = %q, want %q", call.transcript, conv.Transcript)
	}
}

func TestProcess_ReviewerOverturnsFlag(t *testing.T) {
	reporter := &stubReporter{}
	reviewer := &stubReviewer{verdict: Verdict{Violation: false}}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, reviewer, reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 0 {
		t.Fatal("overturned flags must not be reported")
	}
}

func TestProcess_ReviewerErrorDropsConversation(t *testing.T) {
	reporter := &stubReporter{}
	reviewer := &stubReviewer{err: errors.New("upstream down")}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, reviewer, reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 0 {
		t.Fatal("failed review must not be reported")
	}
}

func TestProcess_EachPassGetsItsOwnTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.SafeguardTimeout = time.Minute
	cfg.SafeguardReviewTimeout = 2 * time.Minute
	classifier := &stubClassifier{verdict: Verdict{Violation: true}}
	reviewer := confirmingReviewer()
	svc := NewService(cfg, classifier, reviewer, &stubReporter{})

	start := time.Now()
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))

	classifyDeadline, ok := classifier.ctxs[0].Deadline()
	if !ok {
		t.Fatal("classifier context must carry a deadline")
	}
	reviewDeadline, ok := reviewer.calls[0].ctx.Deadline()
	if !ok {
		t.Fatal("reviewer context must carry a deadline")
	}
	for name, want := range map[string]struct {
		deadline time.Time
		timeout  time.Duration
	}{
		"classifier": {classifyDeadline, cfg.SafeguardTimeout},
		"reviewer":   {reviewDeadline, cfg.SafeguardReviewTimeout},
	} {
		if d := want.deadline.Sub(start); d <= want.timeout-time.Second || d > want.timeout+time.Second {
			t.Fatalf("%s deadline %v from start, want ~%v", name, d, want.timeout)
		}
	}
}

func TestProcess_RetriesReportOnFailure(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts - 1}
	svc := flaggingService(reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 1 || reporter.calls != reportAttempts {
		t.Fatalf("reported = %d, calls = %d", len(reporter.reported), reporter.calls)
	}
}

func TestProcess_GivesUpAfterMaxReportAttempts(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts + 5}
	svc := flaggingService(reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if reporter.calls != reportAttempts {
		t.Fatalf("calls = %d, want exactly %d", reporter.calls, reportAttempts)
	}
	if len(reporter.reported) != 0 {
		t.Fatal("nothing must be reported after exhausting attempts")
	}
}

func TestProcess_ReportRetryWaitsBetweenAttempts(t *testing.T) {
	reporter := &stubReporter{failures: 1}
	svc := flaggingService(reporter)
	var waits []time.Duration
	svc.sleep = func(_ context.Context, d time.Duration) { waits = append(waits, d) }
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(waits) != 1 || waits[0] != reportRetryDelay {
		t.Fatalf("waits = %v, want one wait of %v between the failed and successful attempt", waits, reportRetryDelay)
	}
}

func TestProcess_ReportRetryStopsOnCancelledContext(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts + 5}
	svc := flaggingService(reporter)
	ctx, cancel := context.WithCancel(context.Background())
	svc.sleep = func(context.Context, time.Duration) { cancel() }

	svc.process(ctx, mustConversation(t, "u1", "["+turnOne+"]"))
	if reporter.calls != 1 {
		t.Fatalf("calls = %d, want 1: the retry loop must stop once the context is cancelled", reporter.calls)
	}
}

func TestRunWorker_DrainsQueueThenIdlesUntilCancelled(t *testing.T) {
	reporter := &stubReporter{}
	svc := flaggingService(reporter)
	ctx, cancel := context.WithCancel(context.Background())
	idled := make(chan time.Duration, 1)
	svc.sleep = func(_ context.Context, d time.Duration) {
		select {
		case idled <- d:
		default:
		}
		cancel()
	}
	for _, cred := range []string{"u1", "u2", "u3"} {
		svc.queue.Push(mustConversation(t, cred, "["+turnOne+"]"))
	}

	done := make(chan struct{})
	go func() {
		svc.RunWorker(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker must exit once its context is cancelled")
	}

	if d := <-idled; d != idlePollInterval {
		t.Fatalf("idle wait = %v, want %v", d, idlePollInterval)
	}
	if calls, reported := reporter.snapshot(); calls != 3 || len(reported) != 3 {
		t.Fatalf("calls = %d, reported = %d, want every queued conversation processed before idling", calls, len(reported))
	}
	if svc.queue.Len() != 0 {
		t.Fatal("queue must be drained")
	}
}

func TestRunWorker_ReturnsImmediatelyWhenAlreadyCancelled(t *testing.T) {
	reporter := &stubReporter{}
	svc := flaggingService(reporter)
	svc.queue.Push(mustConversation(t, "u1", "["+turnOne+"]"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.RunWorker(ctx)
	if calls, _ := reporter.snapshot(); calls != 0 {
		t.Fatal("a cancelled worker must not pick up work")
	}
}
