package webshell

import (
	"context"
	"net/http"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// GetIssueFunc performs the daemon-side detail read. The Web layer only renders
// the answer; every rule (confirmation, state machine, audit, trust) stays in
// the daemon's DetailService.
type GetIssueFunc func(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error)

// serveIssue returns the whole detail document for one issue id.
func (s *Server) serveIssue(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	if issueID == "" || len(issueID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_request", "a single issue id is required")
		return
	}
	if s.detail == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the detail view is not available in this launch")
		return
	}
	response, err := s.detail(r.Context(), contract.GetIssueRequest{SchemaVersion: "v1", IssueID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", "no issue has that id")
			return
		}
		writeError(w, http.StatusBadGateway, "unavailable", "the errkb core is not reachable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
