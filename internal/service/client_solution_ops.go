//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// SetSolutionStatus relays the state-machine transition through the pipe.
func (c *Client) SetSolutionStatus(ctx context.Context, request contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error) {
	payload, err := c.Call(ctx, contract.OperationSetSolutionStatus, request)
	if err != nil {
		return contract.SetSolutionStatusResponse{}, err
	}
	var response contract.SetSolutionStatusResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.SetSolutionStatusResponse{}, fmt.Errorf("decode the set_solution_status result: %w", err)
	}
	return response, nil
}

// DeleteSolution relays the confirmation-gated deletion through the pipe.
func (c *Client) DeleteSolution(ctx context.Context, request contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error) {
	payload, err := c.Call(ctx, contract.OperationDeleteSolution, request)
	if err != nil {
		return contract.DeleteSolutionResponse{}, err
	}
	var response contract.DeleteSolutionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.DeleteSolutionResponse{}, fmt.Errorf("decode the delete_solution result: %w", err)
	}
	return response, nil
}
