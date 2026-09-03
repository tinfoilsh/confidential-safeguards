package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	cp := NewControlPlane(server.URL + "/")
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

	if err := NewControlPlane(server.URL).ReportViolation(context.Background(), Violation{}); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}
