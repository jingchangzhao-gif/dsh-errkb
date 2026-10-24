// The capture gate enforces the settings before a write happens. The settings
// page model and the daemon must agree on one rule; this file is the daemon
// side of that rule.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// CaptureGate builds the gate from a validated configuration. A disabled global
// capture stops every write; paused connectors and projects stop only their
// own. Empty keys match nothing, so a missing project never disables capture.
func CaptureGate(config contract.Config) func(ctx context.Context, connectorID, projectKey string) error {
	if config.Capture.Enabled != nil && !*config.Capture.Enabled {
		return func(context.Context, string, string) error {
			return fmt.Errorf("capture is paused in the settings")
		}
	}
	connectors := append([]string(nil), config.Capture.PausedConnectors...)
	projects := append([]string(nil), config.Capture.PausedProjects...)
	return func(_ context.Context, connectorID, projectKey string) error {
		for _, name := range connectors {
			if name != "" && strings.EqualFold(name, connectorID) {
				return fmt.Errorf("capture is paused for connector %q", connectorID)
			}
		}
		for _, name := range projects {
			if name != "" && strings.EqualFold(name, projectKey) {
				return fmt.Errorf("capture is paused for project %q", projectKey)
			}
		}
		return nil
	}
}
