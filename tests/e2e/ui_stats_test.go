//go:build windows

// The EKB-059 acceptance record: the Web toolbox really serves measured
// statistics and audit data, shows the settings page without secrets, and
// previews a deletion without writing anything until it is confirmed.
package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

type uiStatsEnvelope struct {
	Stats struct {
		Retrieval struct {
			Rounds      int64 `json:"Rounds"`
			FalseMerges int64 `json:"FalseMerges"`
		} `json:"Retrieval"`
		Savings struct {
			Assumption string `json:"Assumption"`
		} `json:"Savings"`
	} `json:"stats"`
}

type uiSettingsView struct {
	Status  string `json:"status"`
	Capture struct {
		Enabled bool `json:"enabled"`
	} `json:"Capture"`
	Connectors []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	} `json:"connectors"`
	Retention struct {
		SampleDays int `json:"sample_days"`
	} `json:"Retention"`
}

func startToolbox(t *testing.T, binary, dsn, configPath, pipeName string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	command := exec.Command(binary, "ui", "--web", "--no-browser",
		"--port", fmtInt(port), "--dsn", dsn, "--migrations", migrationsDir(t), "--config", configPath)
	command.Env = append(os.Environ(), "ERRKB_PIPE="+pipeName)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("capture the toolbox output: %v", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatalf("start the toolbox: %v", err)
	}
	stop := func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}

	scanner := bufio.NewScanner(stdout)
	urlLine := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "toolbox: ") {
				urlLine <- strings.TrimSpace(strings.TrimPrefix(line, "toolbox: "))
				return
			}
		}
		urlLine <- ""
	}()
	select {
	case line := <-urlLine:
		if line == "" {
			stop()
			t.Fatalf("the toolbox did not report its URL")
		}
		return line, stop
	case <-time.After(60 * time.Second):
		stop()
		t.Fatal("the toolbox did not start within 60 s")
	}
	return "", stop
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func lastToken(t *testing.T, pageURL string) string {
	t.Helper()
	index := strings.LastIndex(pageURL, "token=")
	if index < 0 {
		t.Fatalf("the toolbox URL %q carries no token", pageURL)
	}
	return pageURL[index+len("token="):]
}

func callJSON(t *testing.T, method, url, token string, body io.Reader) ([]byte, int) {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-Errkb-Token", token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	return payload, response.StatusCode
}

func TestToolboxStatisticsAndDeletionPreview(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the real toolbox acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := "errkb_ui_" + fmtInt(int(time.Now().UnixNano()))
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create the acceptance schema: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	}()
	scopedDSN := withSearchPath(dsn, schema)
	conn, err := pgx.Connect(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("connect to the acceptance schema: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := database.Migrate(ctx, conn, migrationsDir(t)); err != nil {
		t.Fatalf("migrate the acceptance schema: %v", err)
	}

	var connectorID, sessionID string
	if err := conn.QueryRow(ctx, `insert into connectors (name, version, channel, capabilities, source, actor)
		values ('e2e-ui', '0.0.0', 'B', '{}'::jsonb, 'e2e', 'e2e') returning id::text`).Scan(&connectorID); err != nil {
		t.Fatalf("seed the connector: %v", err)
	}
	if err := conn.QueryRow(ctx, `insert into sessions (connector_id, external_id, started_at, source, actor)
		values ($1, 'e2e-ui-session', now(), 'e2e', 'e2e') returning id::text`, connectorID).Scan(&sessionID); err != nil {
		t.Fatalf("seed the session: %v", err)
	}

	binary := buildBinary(t)
	pipeName := `\\.\pipe\errkb-ui-e2e-` + fmtInt(int(time.Now().UnixNano()))
	lockName := `Local\errkb-ui-e2e-` + fmtInt(int(time.Now().UnixNano()))
	dataDir := t.TempDir()
	startDaemon(t, binary, scopedDSN, pipeName, lockName, dataDir)

	if output, code := runCLI(t, binary, pipeName, "run", "--connector", connectorID, "--session", sessionID,
		"--project", e2eProject, "--", "cmd", "/c", "exit 7"); code != 7 {
		t.Fatalf("the recording run exited %d, want 7:\n%s", code, output)
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	pageURL, stop := startToolbox(t, binary, scopedDSN, configPath, pipeName)
	defer stop()
	token := lastToken(t, pageURL)
	base := strings.TrimSuffix(pageURL, "/?token="+token)

	// Wait for the pages to become reachable after the daemon settles.
	deadline := time.Now().Add(45 * time.Second)
	for {
		if _, code := callJSON(t, http.MethodGet, base+"/api/settings", token, nil); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the toolbox never answered /api/settings")
		}
		time.Sleep(300 * time.Millisecond)
	}

	payload, code := callJSON(t, http.MethodGet, base+"/api/stats", token, nil)
	if code != http.StatusOK {
		t.Fatalf("stats status = %d, body = %s", code, payload)
	}
	var envelope uiStatsEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if envelope.Stats.Retrieval.FalseMerges != 0 {
		t.Fatalf("false merges = %d, want 0", envelope.Stats.Retrieval.FalseMerges)
	}
	if strings.TrimSpace(envelope.Stats.Savings.Assumption) == "" {
		t.Fatal("the savings assumption must be visible word for word")
	}

	payload, code = callJSON(t, http.MethodGet, base+"/api/audit?limit=5", token, nil)
	if code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", code, payload)
	}
	if !strings.Contains(string(payload), `"Limit":5`) {
		t.Fatalf("the audit page ignored the limit: %s", payload)
	}

	payload, code = callJSON(t, http.MethodGet, base+"/api/settings", token, nil)
	if code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", code, payload)
	}
	var view uiSettingsView
	if err := json.Unmarshal(payload, &view); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !view.Capture.Enabled {
		t.Fatal("capture should start enabled")
	}
	found := false
	for _, connector := range view.Connectors {
		if connector.Name == "e2e-ui" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the settings page does not list the recording connector: %+v", view.Connectors)
	}
	if view.Retention.SampleDays == 0 {
		t.Fatalf("the retention period was not loaded: %+v", view.Retention)
	}
	for _, forbidden := range []string{"postgres://", "password"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("the settings page leaks %q: %s", forbidden, payload)
		}
	}

	// A preview must not delete anything; a cancel must keep the row.
	payload, code = callJSON(t, http.MethodPost, base+"/api/deletions/preview", token,
		strings.NewReader(`{"project_key":"`+e2eProject+`"}`))
	if code != http.StatusOK || !strings.Contains(string(payload), "issue") {
		t.Fatalf("preview status = %d, body = %s", code, payload)
	}
	if _, code := callJSON(t, http.MethodPost, base+"/api/deletions/cancel", token, strings.NewReader(`{}`)); code != http.StatusOK {
		t.Fatalf("cancel status = %d", code)
	}
	var issues int
	if err := conn.QueryRow(ctx, "select count(*) from issues").Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if issues != 1 {
		t.Fatalf("issues after a cancelled preview = %d, want 1", issues)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("the toolbox should have written its settings file: %v", err)
	}
}
