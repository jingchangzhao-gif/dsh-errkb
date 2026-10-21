package webshell

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// RecordSolutionFunc appends a solution version through the daemon. Every rule
// (confirmation, redaction, state machine, audit) stays in the daemon.
type RecordSolutionFunc func(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error)

// SetStatusFunc changes one issue status through the daemon.
type SetStatusFunc func(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error)

// EnableWrites wires the four write operations the detail page uses. Calling it
// with nil keeps the page read-only and answers 503 for write attempts.
func (s *Server) EnableWrites(
	record RecordSolutionFunc,
	setStatus SetStatusFunc,
	setSolutionStatus SetSolutionStatusFunc,
	deleteSolution DeleteSolutionFunc,
) {
	s.record = record
	s.setStatus = setStatus
	s.setSolutionStatus = setSolutionStatus
	s.deleteSolution = deleteSolution
}

func (s *Server) serveVersion(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Body          string                        `json:"body"`
		Preconditions string                        `json:"preconditions,omitempty"`
		Environment   *contract.SolutionEnvironment `json:"environment,omitempty"`
		Source        string                        `json:"source,omitempty"`
	}
	if err := decodeStrictBody(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if issueID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a single issue id is required")
		return
	}
	if s.record == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the write path is not available in this launch")
		return
	}
	request := contract.RecordSolutionRequest{
		SchemaVersion: "v1",
		IssueID:       issueID,
		Body:          input.Body,
		Preconditions: input.Preconditions,
		Environment:   input.Environment,
		Source:        input.Source,
		Actor:         "toolbox",
	}
	response, err := s.record(r.Context(), request)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) serveIssueStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Status string `json:"status"`
	}
	if err := decodeStrictBody(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if issueID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a single issue id is required")
		return
	}
	if s.setStatus == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the write path is not available in this launch")
		return
	}
	request := contract.SetStatusRequest{SchemaVersion: "v1", IssueID: issueID, Status: input.Status}
	response, err := s.setStatus(r.Context(), request)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// decodeStrictBody rejects unknown fields and trailing data so a typo never
// silently changes nothing.
func decodeStrictBody(w http.ResponseWriter, r *http.Request, target any) error {
	body := http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("the request body is empty")
		}
		return errors.New("the request body is not valid JSON for this field set")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("the request body has trailing data")
	}
	return nil
}

// writeCoreError keeps the daemon's error vocabulary; the Web layer never
// invents its own classification.
func writeCoreError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "not_found") || strings.Contains(message, "not found"):
		writeError(w, http.StatusNotFound, "not_found", "no such row")
	case strings.Contains(message, "confirmation") || strings.Contains(message, "conflict") ||
		strings.Contains(message, "preview") || strings.Contains(message, "confirm"):
		writeError(w, http.StatusConflict, "conflict", message)
	case strings.Contains(message, "invalid") || strings.Contains(message, "required") || strings.Contains(message, "unknown") || strings.Contains(message, "bad "):
		writeError(w, http.StatusBadRequest, "invalid_request", message)
	default:
		writeError(w, http.StatusBadGateway, "unavailable", "the errkb core did not answer")
	}
}

// SetSolutionStatusFunc moves one solution through its closed state machine.
type SetSolutionStatusFunc func(ctx context.Context, request contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error)

// DeleteSolutionFunc removes one solution after the second confirmation.
type DeleteSolutionFunc func(ctx context.Context, request contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error)

func (s *Server) serveSolutionStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	solutionID := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Status    string `json:"status"`
		Confirmed bool   `json:"confirmed,omitempty"`
	}
	if err := decodeStrictBody(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if solutionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a single solution id is required")
		return
	}
	if s.setSolutionStatus == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the write path is not available in this launch")
		return
	}
	request := contract.SetSolutionStatusRequest{
		SchemaVersion: "v1",
		SolutionID:    solutionID,
		Status:        input.Status,
		Confirmed:     input.Confirmed,
	}
	response, err := s.setSolutionStatus(r.Context(), request)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) serveSolutionDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	solutionID := strings.TrimSpace(r.PathValue("id"))
	var input struct {
		Confirmed bool `json:"confirmed,omitempty"`
	}
	if err := decodeStrictBody(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if solutionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a single solution id is required")
		return
	}
	if s.deleteSolution == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the write path is not available in this launch")
		return
	}
	request := contract.DeleteSolutionRequest{
		SchemaVersion: "v1",
		SolutionID:    solutionID,
		Confirmed:     input.Confirmed,
	}
	response, err := s.deleteSolution(r.Context(), request)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
