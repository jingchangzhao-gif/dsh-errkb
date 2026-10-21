package webshell

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/ui"
)

type fakeSettingsStore struct {
	config contract.Config
	saves  int
	fail   error
}

func (f *fakeSettingsStore) Load() (contract.Config, error) { return f.config, f.fail }

func (f *fakeSettingsStore) Save(config contract.Config) error {
	if f.fail != nil {
		return f.fail
	}
	f.config = config
	f.saves++
	return nil
}

type fakeSettingsData struct {
	connectors []ui.ConnectorSettings
	preview    database.DeleteCounts
	deleted    database.DeleteCounts
	applied    database.RetentionReport
}

func (f *fakeSettingsData) Connectors(context.Context) ([]ui.ConnectorSettings, error) {
	return f.connectors, nil
}

func (f *fakeSettingsData) PreviewDelete(context.Context, database.DeleteScope) (database.DeleteCounts, error) {
	return f.preview, nil
}

func (f *fakeSettingsData) DeleteScoped(context.Context, database.DeleteScope) (database.DeleteCounts, error) {
	return f.deleted, nil
}

func (f *fakeSettingsData) ApplyRetention(context.Context, database.RetentionPolicy, time.Time) (database.RetentionReport, error) {
	return f.applied, nil
}

func settingsPointer[T any](value T) *T { return &value }

func settingsConfig() contract.Config {
	enabled := true
	disabled := false
	return contract.Config{
		Database: contract.DatabaseConfig{DSN: "postgres://postgres:very-secret@127.0.0.1:5432/errkb?sslmode=disable"},
		Service: contract.ServiceConfig{
			Pipe: contract.PipeConfig{Name: "errkb", Enabled: &enabled},
			HTTP: contract.HTTPConfig{Enabled: &disabled, Host: "127.0.0.1", Token: "a-very-secret-token"},
		},
		Capture:   contract.CaptureConfig{Enabled: &enabled},
		Notices:   contract.NoticesConfig{BeforeDiagnosis: &enabled},
		Retention: contract.RetentionConfig{SampleDays: settingsPointer(7), NormalizedDays: settingsPointer(30), AuditDays: settingsPointer(365)},
		Search:    contract.SearchConfig{Limit: settingsPointer(3)},
		Timeout:   contract.TimeoutConfig{CaptureMS: settingsPointer(2000), SearchMS: settingsPointer(1500), MCPMS: settingsPointer(5000)},
		Offline:   contract.OfflineConfig{ReplayIntervalMS: settingsPointer(5000)},
	}
}

func newSettingsShell(t *testing.T, store *fakeSettingsStore, data *fakeSettingsData) (*Server, string) {
	t.Helper()
	server, httpServer := newTestServer(t)
	t.Cleanup(httpServer.Close)
	server.EnableSettings(ui.NewSettingsPage(store, data))
	return server, httpServer.URL
}

func TestSettingsViewHidesSecrets(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	data := &fakeSettingsData{connectors: []ui.ConnectorSettings{{Name: "claude-code", Channel: "A", Version: "2.1.282", Enabled: true}}}
	server, base := newSettingsShell(t, store, data)
	payload, code := doJSON(t, http.MethodGet, base+"/api/settings", server.Token(), nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, payload)
	}
	body := string(payload)
	for _, forbidden := range []string{"very-secret", "a-very-secret-token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the settings view leaks %q: %s", forbidden, body)
		}
	}
	var view struct {
		Capture struct {
			Enabled bool `json:"enabled"`
		} `json:"capture"`
		Connectors []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"connectors"`
		Retention struct {
			SampleDays     int `json:"sample_days"`
			NormalizedDays int `json:"normalized_days"`
			AuditDays      int `json:"audit_days"`
		} `json:"retention"`
		HTTP struct {
			TokenSet bool `json:"token_set"`
		} `json:"http"`
	}
	if err := json.Unmarshal(payload, &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Connectors) != 1 || view.Connectors[0].Name != "claude-code" {
		t.Fatalf("connectors = %+v", view.Connectors)
	}
	if view.Retention.SampleDays != 7 || view.Retention.NormalizedDays != 30 || view.Retention.AuditDays != 365 {
		t.Fatalf("retention = %+v", view.Retention)
	}
	if !view.HTTP.TokenSet {
		t.Fatal("http.token_set should be true")
	}
}

func TestConnectorToggleSavesAndRejectsUnknown(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	data := &fakeSettingsData{connectors: []ui.ConnectorSettings{{Name: "claude-code", Channel: "A", Version: "2.1.282", Enabled: true}}}
	server, base := newSettingsShell(t, store, data)
	payload, code := doJSON(t, http.MethodPost, base+"/api/settings/connectors", server.Token(), strings.NewReader(`{"name":"claude-code","enabled":false}`))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, payload)
	}
	if store.saves != 1 {
		t.Fatalf("saves = %d, want 1", store.saves)
	}
	if !contains(store.config.Capture.PausedConnectors, "claude-code") {
		t.Fatalf("paused connectors = %v", store.config.Capture.PausedConnectors)
	}
	if _, code := doJSON(t, http.MethodPost, base+"/api/settings/connectors", server.Token(), strings.NewReader(`{"name":"nope","enabled":false}`)); code != http.StatusBadRequest {
		t.Fatalf("unknown connector status = %d, want 400", code)
	}
}

func TestRetentionRejectsNegativeWithoutWriting(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	server, base := newSettingsShell(t, store, &fakeSettingsData{})
	if _, code := doJSON(t, http.MethodPost, base+"/api/settings/retention", server.Token(), strings.NewReader(`{"sample_days":-1,"normalized_days":30,"audit_days":365}`)); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if store.saves != 0 {
		t.Fatalf("saves = %d, want 0", store.saves)
	}
}

func TestDeletionPreviewThenConfirmAndDrift(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	data := &fakeSettingsData{
		preview: database.DeleteCounts{Issues: 3, Events: 4, Occurrences: 4},
		deleted: database.DeleteCounts{Issues: 3, Events: 4, Occurrences: 4},
	}
	server, base := newSettingsShell(t, store, data)
	payload, code := doJSON(t, http.MethodPost, base+"/api/deletions/confirm", server.Token(), strings.NewReader(`{}`))
	if code != http.StatusConflict {
		t.Fatalf("confirm without preview: status = %d, body = %s", code, payload)
	}
	payload, code = doJSON(t, http.MethodPost, base+"/api/deletions/preview", server.Token(), strings.NewReader(`{"project_key":"p1"}`))
	if code != http.StatusOK || !strings.Contains(string(payload), `"issues":3`) {
		t.Fatalf("preview: status = %d, body = %s", code, payload)
	}
	payload, code = doJSON(t, http.MethodPost, base+"/api/deletions/confirm", server.Token(), strings.NewReader(`{}`))
	if code != http.StatusOK || !strings.Contains(string(payload), `"events":4`) {
		t.Fatalf("confirm: status = %d, body = %s", code, payload)
	}
	if _, code := doJSON(t, http.MethodPost, base+"/api/deletions/confirm", server.Token(), strings.NewReader(`{}`)); code != http.StatusConflict {
		t.Fatalf("second confirm: status = %d, want 409", code)
	}
}

func TestRetentionApplyRequiresConfirmation(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	data := &fakeSettingsData{applied: database.RetentionReport{SamplesCleared: 1, EventsRemoved: 2, AuditRowsRemoved: 3}}
	server, base := newSettingsShell(t, store, data)
	if _, code := doJSON(t, http.MethodPost, base+"/api/settings/retention/apply", server.Token(), strings.NewReader(`{}`)); code != http.StatusConflict {
		t.Fatalf("unconfirmed status = %d, want 409", code)
	}
	payload, code := doJSON(t, http.MethodPost, base+"/api/settings/retention/apply", server.Token(), strings.NewReader(`{"confirmed":true}`))
	if code != http.StatusOK || !strings.Contains(string(payload), `"events_removed":2`) {
		t.Fatalf("status = %d, body = %s", code, payload)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
