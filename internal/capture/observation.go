// The connector SDK capture pipeline (EKB-038 rollback). A connector hands over
// a raw observation; classification, redaction, fingerprinting and storage stay
// in the core, so an adapter never reimplements them (plan section 3.2).
package capture

import (
	"context"
	"crypto/md5"
	"fmt"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// observationChannelDefault is channel A, the hook/plugin structured-event
// channel (plan section 3.1). An adapter may report another level explicitly.
const observationChannelDefault = "A"

// observationConfidence is the default confidence of a first report; the
// adapter may refine it in the observation.
const observationConfidence = 0.5

// CaptureObservation turns one connector observation into a stored event. The
// connector name and session key are resolved to uuids by the core; a replay
// carrying the same observation_id reuses the same event id, so the database's
// ON CONFLICT rule answers with the stored identity instead of counting twice.
func CaptureObservation(ctx context.Context, conn *pgx.Conn, request contract.CaptureRequest) (contract.CaptureResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.CaptureResponse{}, fmt.Errorf("capture: %w", err)
	}
	channel := request.Channel
	if channel == "" {
		channel = observationChannelDefault
	}
	connectorID, err := database.EnsureConnector(ctx, conn, request.ConnectorID, channel, "")
	if err != nil {
		return contract.CaptureResponse{}, err
	}
	sessionID, err := database.EnsureSession(ctx, conn, connectorID, request.SessionID, request.Observation.ProjectKey)
	if err != nil {
		return contract.CaptureResponse{}, err
	}
	event, err := buildObservationEvent(request, connectorID, sessionID, channel)
	if err != nil {
		return contract.CaptureResponse{}, err
	}
	// A replay carrying the same observation_id must not become a second
	// appearance: EKB-020 counts every record call, so the capture layer
	// absorbs the retransmission before it reaches the database.
	if request.ObservationID != "" {
		stored, err := database.StoredEventByEventID(ctx, conn, event.EventID)
		if err != nil {
			return contract.CaptureResponse{}, err
		}
		if stored != nil {
			return captureResponse(stored), nil
		}
	}
	recorded, err := database.RecordEvent(ctx, conn, event)
	if err != nil {
		return contract.CaptureResponse{}, err
	}
	return captureResponse(&database.StoredEvent{
		EventID:      recorded.EventID,
		OccurrenceID: recorded.OccurrenceID,
		IssueID:      recorded.IssueID,
		Fingerprint:  recorded.Fingerprint,
		Created:      recorded.Created,
	}), nil
}

// captureResponse maps the storage identity onto the connector SDK envelope.
func captureResponse(stored *database.StoredEvent) contract.CaptureResponse {
	return contract.CaptureResponse{
		SchemaVersion: contract.ConnectorSDKVersion,
		EventID:       stored.EventID,
		OccurrenceID:  stored.OccurrenceID,
		IssueID:       stored.IssueID,
		Fingerprint:   stored.Fingerprint,
		Created:       stored.Created,
	}
}

// buildObservationEvent applies the shared pipeline: headline, classify,
// redact, normalize, fingerprint. Raw text never reaches the database.
func buildObservationEvent(request contract.CaptureRequest, connectorID, sessionID, channel string) (contract.ErrorEvent, error) {
	observation := request.Observation
	headline := core.ExtractHeadline(observation.Message)
	code := observation.ErrorCode
	if code == "" {
		code = headline.Code
	}
	classification := core.Classify(core.ClassifyInput{
		Source:   observation.Source,
		Code:     code,
		Message:  headline.Line,
		Command:  observation.Command,
		Tool:     observation.ToolName,
		ExitCode: observation.ExitCode,
	})
	line := redact.Text(headline.Line, redact.Public)
	if strings.TrimSpace(line) == "" {
		line = fmt.Sprintf("errkb capture: %s failure without a message", observation.Source)
	}
	confidence := observationConfidence
	if observation.Confidence != nil {
		confidence = *observation.Confidence
	}
	osName := observation.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	eventID := newEventID()
	if request.ObservationID != "" {
		eventID = observationEventID(request.ConnectorID, request.ObservationID)
	}
	evidence := redact.Text(observation.Evidence, redact.Public)
	if evidence == "" {
		evidence = fmt.Sprintf("connector %s %s", request.ConnectorID, observation.Source)
	}
	transient := classification.Transient
	normalized := core.Normalize(line)
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           eventID,
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        observation.ObservedAt,
		Channel:           channel,
		Category:          observation.Source,
		Severity:          string(classification.Severity),
		Transient:         &transient,
		Headline:          line,
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(observation.Message, redact.Public),
		ErrorCode:         code,
		Command:           observation.Command,
		ExitCode:          observation.ExitCode,
		ToolName:          observation.ToolName,
		OS:                osName,
		Shell:             observation.Shell,
		RuntimeVersions:   observation.RuntimeVersions,
		ProjectKey:        observation.ProjectKey,
		Fingerprint:       core.Signature(observation.Source, normalized),
		Evidence:          evidence,
		Confidence:        &confidence,
	}
	if err := event.Validate(); err != nil {
		return contract.ErrorEvent{}, fmt.Errorf("capture: build event: %w", err)
	}
	return event, nil
}

// observationEventID derives a deterministic event id from the adapter's
// observation id, so a replay after a lost acknowledgement lands on the same
// events row and the database's ON CONFLICT rule answers with the stored
// identity (EKB-020). Two connectors cannot collide because the name is part of
// the input.
func observationEventID(connectorName, observationID string) string {
	sum := md5.Sum([]byte("errkb-observation|" + connectorName + "|" + observationID))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
