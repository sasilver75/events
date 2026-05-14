package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusHandlerServesStatusFile(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "status.json")
	statusJSON := `{"generated_at":"2026-05-14T12:00:00Z","running":[{"identifier":"SAM-60"}]}`
	if err := os.WriteFile(statusFile, []byte(statusJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := newHandler(serverConfig{StatusFile: statusFile})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	running := body["running"].([]any)
	first := running[0].(map[string]any)
	if first["identifier"] != "SAM-60" {
		t.Fatalf("identifier = %v, want SAM-60", first["identifier"])
	}
}

func TestStatusHandlerAggregatesStatusDirectory(t *testing.T) {
	dir := t.TempDir()
	writeStatus := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus("SAM-60-codex.json", `{
		"generated_at":"2026-05-14T12:00:00Z",
		"max_concurrent_agents":1,
		"agent_runner":"codex",
		"linear_access":"host_proxy",
		"running":[{"identifier":"SAM-60","session_id":"session-60"}],
		"recent_runs":[{"identifier":"SAM-59","finished_at":"2026-05-14T11:00:00Z","token_info":{"total_tokens":5}}],
		"claimed_count":1,
		"completed_count":1,
		"codex_totals":{"input_tokens":2,"output_tokens":3,"total_tokens":5,"seconds_running":4}
	}`)
	writeStatus("SAM-61-codex.json", `{
		"generated_at":"2026-05-14T12:01:00Z",
		"max_concurrent_agents":1,
		"agent_runner":"codex",
		"linear_access":"host_proxy",
		"retrying":[{"identifier":"SAM-61","attempt":2}],
		"recent_runs":[{"identifier":"SAM-61","finished_at":"2026-05-14T12:01:00Z","token_info":{"total_tokens":7}}],
		"claimed_count":1,
		"completed_count":1,
		"codex_totals":{"input_tokens":4,"output_tokens":3,"total_tokens":7,"seconds_running":6}
	}`)

	handler := newHandler(serverConfig{StatusDir: dir})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["source_mode"] != "directory" {
		t.Fatalf("source_mode = %v, want directory", body["source_mode"])
	}
	if body["source_count"] != float64(2) {
		t.Fatalf("source_count = %v, want 2", body["source_count"])
	}
	running := body["running"].([]any)
	firstRunning := running[0].(map[string]any)
	if firstRunning["_source_file"] != "SAM-60-codex.json" {
		t.Fatalf("_source_file = %v, want SAM-60-codex.json", firstRunning["_source_file"])
	}
	totals := body["codex_totals"].(map[string]any)
	if totals["total_tokens"] != float64(12) {
		t.Fatalf("total tokens = %v, want 12", totals["total_tokens"])
	}
	recent := body["recent_runs"].([]any)
	firstRecent := recent[0].(map[string]any)
	if firstRecent["identifier"] != "SAM-61" {
		t.Fatalf("first recent identifier = %v, want newest SAM-61", firstRecent["identifier"])
	}
}

func TestStatusHandlerReportsMissingStatusFile(t *testing.T) {
	handler := newHandler(serverConfig{StatusFile: filepath.Join(t.TempDir(), "missing.json")})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "does not exist yet") {
		t.Fatalf("body = %q, want missing-file message", rec.Body.String())
	}
}

func TestStatusHandlerRejectsInvalidJSON(t *testing.T) {
	statusFile := filepath.Join(t.TempDir(), "status.json")
	if err := os.WriteFile(statusFile, []byte(`not-json`), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := newHandler(serverConfig{StatusFile: statusFile})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rec.Body.String(), "not valid JSON") {
		t.Fatalf("body = %q, want invalid JSON message", rec.Body.String())
	}
}

func TestIndexIncludesDashboardHooks(t *testing.T) {
	handler := newHandler(serverConfig{StatusFile: "/tmp/status.json"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		"Symphony Harness Status",
		"id=\"config-toggle\"",
		"id=\"config-drawer\"",
		"id=\"running-body\"",
		"fetch(\"/status\"",
		"fetch(\"/meta\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard HTML missing %q", want)
		}
	}
}

func TestMetaHandlerServesWatchedPath(t *testing.T) {
	handler := newHandler(serverConfig{StatusFile: "/tmp/spur-orchestrator/status.json"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/meta", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var meta metaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	if meta.StatusFile != "/tmp/spur-orchestrator/status.json" {
		t.Fatalf("status file = %q", meta.StatusFile)
	}
	if meta.SourceMode != "file" {
		t.Fatalf("source mode = %q, want file", meta.SourceMode)
	}
	if meta.ServedAt.IsZero() {
		t.Fatal("served_at is zero")
	}
}

func TestMetaHandlerServesWatchedDirectory(t *testing.T) {
	handler := newHandler(serverConfig{StatusDir: "/tmp/spur-orchestrator"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/meta", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var meta metaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	if meta.SourceMode != "directory" {
		t.Fatalf("source mode = %q, want directory", meta.SourceMode)
	}
	if meta.StatusDir != "/tmp/spur-orchestrator" {
		t.Fatalf("status dir = %q", meta.StatusDir)
	}
}
