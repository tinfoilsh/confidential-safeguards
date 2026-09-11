package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

type stubReviewer struct {
	verdict     Verdict
	err         error
	calls       int
	judged      []Verdict
	transcripts []string
	ctxs        []context.Context
}

func (s *stubReviewer) Review(ctx context.Context, transcript string, judge *Verdict) (*Verdict, error) {
	s.calls++
	s.judged = append(s.judged, *judge)
	s.transcripts = append(s.transcripts, transcript)
	s.ctxs = append(s.ctxs, ctx)
	if s.err != nil {
		return nil, s.err
	}
	return &s.verdict, nil
}

func confirmingReviewer() *stubReviewer {
	return &stubReviewer{verdict: Verdict{Violation: true}}
}

type stubReporter struct {
	reported []Violation
	failures int
	calls    int
}

func (s *stubReporter) ReportViolation(_ context.Context, v Violation) error {
	s.calls++
	if s.calls <= s.failures {
		return errors.New("control plane unavailable")
	}
	s.reported = append(s.reported, v)
	return nil
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

func ingest(svc *Service, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	svc.HandleIngest(rec, req)
	return rec
}

func TestHandleIngest_Validation(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, confirmingReviewer(), &stubReporter{})
	for name, body := range map[string]string{
		"invalid json":    `{`,
		"missing cred":    `{"messages":[` + turnOne + `]}`,
		"missing turns":   `{"credential":"u1"}`,
		"malformed turns": `{"credential":"u1","messages":[{"content":"x"}]}`,
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

func TestProcess_RetriesReportOnFailure(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts - 1}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, confirmingReviewer(), reporter)
	svc.sleep = func(context.Context, time.Duration) {}
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 1 || reporter.calls != reportAttempts {
		t.Fatalf("reported = %d, calls = %d", len(reporter.reported), reporter.calls)
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
	if reviewer.calls != 0 {
		t.Fatal("reviewer must not run on clean conversations")
	}
}

func TestProcess_ClassifierErrorDropsConversation(t *testing.T) {
	reporter := &stubReporter{}
	svc := NewService(testConfig(), &stubClassifier{err: errors.New("upstream down")}, confirmingReviewer(), reporter)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(reporter.reported) != 0 {
		t.Fatal("failed classification must not be reported")
	}
}

func TestProcess_ReviewerSeesJudgeVerdict(t *testing.T) {
	judge := Verdict{Violation: true, Categories: []string{"self_harm"}, Reason: "encouraged self-harm"}
	reviewer := confirmingReviewer()
	svc := NewService(testConfig(), &stubClassifier{verdict: judge}, reviewer, &stubReporter{})
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if got := reviewer.judged[0]; got.Reason != judge.Reason || len(got.Categories) != 1 || got.Categories[0] != "self_harm" {
		t.Fatalf("reviewer saw %+v, want %+v", got, judge)
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

func TestProcess_ReviewerReceivesExactTranscript(t *testing.T) {
	reviewer := confirmingReviewer()
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, reviewer, &stubReporter{})
	conv := mustConversation(t, "u1", "["+turnTwo+"]")
	svc.process(context.Background(), conv)
	if len(reviewer.transcripts) != 1 || reviewer.transcripts[0] != conv.Transcript {
		t.Fatalf("reviewer transcript = %q, want %q", reviewer.transcripts, conv.Transcript)
	}
}

func TestProcess_ReviewerGetsFreshTimeout(t *testing.T) {
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
	reviewDeadline, ok := reviewer.ctxs[0].Deadline()
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

func TestProcess_GivesUpAfterMaxReportAttempts(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts + 5}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, confirmingReviewer(), reporter)
	svc.sleep = func(context.Context, time.Duration) {}
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if reporter.calls != reportAttempts {
		t.Fatalf("calls = %d, want exactly %d", reporter.calls, reportAttempts)
	}
	if len(reporter.reported) != 0 {
		t.Fatal("nothing must be reported after exhausting attempts")
	}
}

func TestProcess_ReportRetryStopsOnCancelledContext(t *testing.T) {
	reporter := &stubReporter{failures: reportAttempts + 5}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, confirmingReviewer(), reporter)
	ctx, cancel := context.WithCancel(context.Background())
	svc.sleep = func(context.Context, time.Duration) { cancel() }

	svc.process(ctx, mustConversation(t, "u1", "["+turnOne+"]"))
	if reporter.calls != 1 {
		t.Fatalf("calls = %d, want 1: the retry loop must stop once the context is cancelled", reporter.calls)
	}
}
