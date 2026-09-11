package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	controlPlaneViolationPath = "/api/internal/safeguards/violations"
	controlPlaneTimeout       = 10 * time.Second
)

type Violation struct {
	Credential     string `json:"credential"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// ControlPlaneReporter reports violations to the control plane. The request
// carries no service credential of its own: the reported user credential is
// verified by the control plane and is sufficient to authenticate the report.
type ControlPlaneReporter struct {
	endpoint string
	client   *http.Client
}

func NewControlPlaneReporter(baseURL string) *ControlPlaneReporter {
	return &ControlPlaneReporter{
		endpoint: strings.TrimRight(baseURL, "/") + controlPlaneViolationPath,
		client:   &http.Client{Timeout: controlPlaneTimeout},
	}
}

func (c *ControlPlaneReporter) ReportViolation(ctx context.Context, v Violation) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("control plane request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane returned status %d", resp.StatusCode)
	}
	return nil
}
