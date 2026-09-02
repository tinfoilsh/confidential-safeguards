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
	controlPlaneSecretHeader  = "X-Tinfoil-Safeguards-Secret"
	controlPlaneViolationPath = "/api/internal/safeguards/violations"
	controlPlaneTimeout       = 10 * time.Second
)

type Violation struct {
	Credential     string `json:"credential"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type Notifier interface {
	ReportViolation(ctx context.Context, v Violation) error
}

type ControlPlane struct {
	endpoint string
	secret   string
	client   *http.Client
}

func NewControlPlane(baseURL, secret string) *ControlPlane {
	return &ControlPlane{
		endpoint: strings.TrimRight(baseURL, "/") + controlPlaneViolationPath,
		secret:   secret,
		client:   &http.Client{Timeout: controlPlaneTimeout},
	}
}

func (c *ControlPlane) ReportViolation(ctx context.Context, v Violation) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(controlPlaneSecretHeader, c.secret)

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
