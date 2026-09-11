package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestControlPlane_ReportViolation(t *testing.T) {
	var got *http.Request
	var body Violation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cp := NewControlPlaneReporter(server.URL + "/")
	want := Violation{Credential: "tk_abc", ConversationID: "chat-1"}
	if err := cp.ReportViolation(context.Background(), want); err != nil {
		t.Fatalf("ReportViolation: %v", err)
	}
	if got.Method != http.MethodPost || got.URL.Path != controlPlaneViolationPath {
		t.Fatalf("unexpected request %s %s", got.Method, got.URL.Path)
	}
	if body != want {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
}

func TestControlPlane_RejectsNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	if err := NewControlPlaneReporter(server.URL).ReportViolation(context.Background(), Violation{}); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

// TestViolation_WireFormatIsPinned mechanically enforces the enclave boundary:
// the report that leaves the enclave may contain the credential and the
// conversation id and NOTHING else. If this test fails, a field was added to
// Violation — that is an exfiltration-surface change and needs privacy review.
func TestViolation_WireFormatIsPinned(t *testing.T) {
	body, err := json.Marshal(Violation{Credential: "c", ConversationID: "id"})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"credential": "c", "conversation_id": "id"}
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("wire format = %v, want exactly %v", wire, want)
	}
	if n := reflect.TypeOf(Violation{}).NumField(); n != 2 {
		t.Fatalf("Violation has %d fields, want 2", n)
	}
}
