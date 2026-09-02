package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoilsh/confidential-safeguards/config"
)

type stubClassifier struct {
	verdict Verdict
	err     error
}

func (s *stubClassifier) Classify(context.Context, string) (*Verdict, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &s.verdict, nil
}

type stubNotifier struct {
	reported []Violation
	failures int
	calls    int
}

func (s *stubNotifier) ReportViolation(_ context.Context, v Violation) error {
	s.calls++
	if s.calls <= s.failures {
		return errors.New("control plane unavailable")
	}
	s.reported = append(s.reported, v)
	return nil
}

func testConfig() *config.Config {
	return &config.Config{
		MaxRequestBytes:    1 << 20,
		MaxTranscriptBytes: 1 << 20,
		SafeguardTimeout:   time.Second,
		QueueTTL:           time.Hour,
		QueueMaxSize:       100,
	}
}

func ingest(svc *Service, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	svc.HandleIngest(rec, req)
	return rec
}

func TestHandleIngest_Validation(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, &stubNotifier{})
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
	svc := NewService(testConfig(), &stubClassifier{}, &stubNotifier{})
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
	svc := NewService(cfg, &stubClassifier{}, &stubNotifier{})
	if rec := ingest(svc, `{"credential":"u1","messages":[`+turnTwo+`]}`); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHandleIngest_Queues(t *testing.T) {
	svc := NewService(testConfig(), &stubClassifier{}, &stubNotifier{})
	if rec := ingest(svc, `{"credential":"u1","messages":[`+turnOne+`]}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	ingest(svc, `{"credential":"u1","messages":[`+turnTwo+`]}`)
	if svc.queue.Len() != 1 {
		t.Fatalf("queue len = %d, want 1 after prefix replacement", svc.queue.Len())
	}
}

func TestProcess_ReportsViolation(t *testing.T) {
	notifier := &stubNotifier{}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true, Category: "cbrn"}}, notifier)
	conv, err := NewConversation("cred-1", "chat-42", json.RawMessage("["+turnTwo+"]"), testMaxTranscript)
	if err != nil {
		t.Fatal(err)
	}
	svc.process(context.Background(), conv)

	want := Violation{Credential: "cred-1", ConversationID: "chat-42"}
	if len(notifier.reported) != 1 || notifier.reported[0] != want {
		t.Fatalf("reported = %+v, want [%+v]", notifier.reported, want)
	}
}

func TestProcess_RetriesReportOnFailure(t *testing.T) {
	notifier := &stubNotifier{failures: reportAttempts - 1}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Violation: true}}, notifier)
	svc.reportRetryDelay = time.Millisecond
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(notifier.reported) != 1 || notifier.calls != reportAttempts {
		t.Fatalf("reported = %d, calls = %d", len(notifier.reported), notifier.calls)
	}
}

func TestProcess_SafeConversationNotReported(t *testing.T) {
	notifier := &stubNotifier{}
	svc := NewService(testConfig(), &stubClassifier{verdict: Verdict{Category: "none"}}, notifier)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(notifier.reported) != 0 {
		t.Fatal("safe conversations must not be reported")
	}
}

func TestProcess_ClassifierErrorDropsConversation(t *testing.T) {
	notifier := &stubNotifier{}
	svc := NewService(testConfig(), &stubClassifier{err: errors.New("upstream down")}, notifier)
	svc.process(context.Background(), mustConversation(t, "u1", "["+turnOne+"]"))
	if len(notifier.reported) != 0 {
		t.Fatal("failed classification must not be reported")
	}
}
