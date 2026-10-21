package service

import (
	"context"
	"errors"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// SetSolutionStatus moves a solution through the closed state machine. The
// destructive transitions require the explicit confirmation the page collects.
func (b *SQLBackend) SetSolutionStatus(ctx context.Context, request contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.SetSolutionStatusResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	if b.pool == nil {
		return contract.SetSolutionStatusResponse{}, APIErrorf(CodeUnavailable, "no database pool is configured")
	}
	err := NewDetailService(b.pool).ChangeSolutionStatus(ctx, request.SolutionID, request.Status, request.Confirmed)
	if err != nil {
		if errors.Is(err, ErrConfirmationRequired) {
			return contract.SetSolutionStatusResponse{}, APIErrorf(CodeConflict, "confirmation required: %v", err)
		}
		if errors.Is(err, ErrDetailNotFound) {
			return contract.SetSolutionStatusResponse{}, APIErrorf(CodeNotFound, "solution %s not found", request.SolutionID)
		}
		return contract.SetSolutionStatusResponse{}, translateBackendError("set_solution_status", err)
	}
	return contract.SetSolutionStatusResponse{
		SchemaVersion: contract.SchemaVersion,
		SolutionID:    request.SolutionID,
		Status:        request.Status,
	}, nil
}

// DeleteSolution removes a solution and its versions; confirmed must be true.
func (b *SQLBackend) DeleteSolution(ctx context.Context, request contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.DeleteSolutionResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	if b.pool == nil {
		return contract.DeleteSolutionResponse{}, APIErrorf(CodeUnavailable, "no database pool is configured")
	}
	err := NewDetailService(b.pool).DeleteSolution(ctx, request.SolutionID, request.Confirmed)
	if err != nil {
		if errors.Is(err, ErrConfirmationRequired) {
			return contract.DeleteSolutionResponse{}, APIErrorf(CodeConflict, "confirmation required: %v", err)
		}
		if errors.Is(err, ErrDetailNotFound) {
			return contract.DeleteSolutionResponse{}, APIErrorf(CodeNotFound, "solution %s not found", request.SolutionID)
		}
		return contract.DeleteSolutionResponse{}, translateBackendError("delete_solution", err)
	}
	return contract.DeleteSolutionResponse{
		SchemaVersion: contract.SchemaVersion,
		SolutionID:    request.SolutionID,
		Deleted:       true,
	}, nil
}
