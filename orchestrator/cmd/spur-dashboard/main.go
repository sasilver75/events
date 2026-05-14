package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultStatusDir = "/tmp/spur-orchestrator"
const defaultAddr = "127.0.0.1:8791"

type serverConfig struct {
	Addr       string
	StatusFile string
	StatusDir  string
}

type metaResponse struct {
	SourceMode string    `json:"source_mode"`
	StatusFile string    `json:"status_file,omitempty"`
	StatusDir  string    `json:"status_dir,omitempty"`
	ServedAt   time.Time `json:"served_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type sourceSummary struct {
	Path          string     `json:"path"`
	File          string     `json:"file"`
	ModTime       time.Time  `json:"mod_time"`
	GeneratedAt   *time.Time `json:"generated_at,omitempty"`
	RunningCount  int        `json:"running_count"`
	RetryingCount int        `json:"retrying_count"`
	HumanCount    int        `json:"human_count"`
	RecentCount   int        `json:"recent_count"`
	Error         string     `json:"error,omitempty"`
}

type statusSnapshot struct {
	GeneratedAt         *time.Time       `json:"generated_at,omitempty"`
	PollIntervalMs      int              `json:"poll_interval_ms,omitempty"`
	MaxConcurrentAgents int              `json:"max_concurrent_agents,omitempty"`
	AgentRunner         string           `json:"agent_runner,omitempty"`
	LinearAccess        string           `json:"linear_access,omitempty"`
	Running             []map[string]any `json:"running,omitempty"`
	Retrying            []map[string]any `json:"retrying,omitempty"`
	NeedsHuman          []map[string]any `json:"needs_human,omitempty"`
	RecentRuns          []map[string]any `json:"recent_runs,omitempty"`
	ClaimedCount        int              `json:"claimed_count,omitempty"`
	CompletedCount      int              `json:"completed_count,omitempty"`
	WorkflowPath        string           `json:"workflow_path,omitempty"`
	WorkflowModTime     *time.Time       `json:"workflow_mod_time,omitempty"`
	CodexTotals         statusTokenInfo  `json:"codex_totals,omitempty"`
	CodexRateLimits     any              `json:"codex_rate_limits,omitempty"`
}

type statusTokenInfo struct {
	InputTokens    int   `json:"input_tokens"`
	OutputTokens   int   `json:"output_tokens"`
	TotalTokens    int   `json:"total_tokens"`
	SecondsRunning int64 `json:"seconds_running"`
}

type aggregateSnapshot struct {
	GeneratedAt         time.Time        `json:"generated_at"`
	SourceMode          string           `json:"source_mode"`
	SourceCount         int              `json:"source_count"`
	Sources             []sourceSummary  `json:"sources"`
	SourceErrors        []sourceSummary  `json:"source_errors,omitempty"`
	PollIntervalMs      int              `json:"poll_interval_ms,omitempty"`
	MaxConcurrentAgents int              `json:"max_concurrent_agents"`
	AgentRunner         string           `json:"agent_runner"`
	LinearAccess        string           `json:"linear_access"`
	Running             []map[string]any `json:"running"`
	Retrying            []map[string]any `json:"retrying"`
	NeedsHuman          []map[string]any `json:"needs_human"`
	RecentRuns          []map[string]any `json:"recent_runs"`
	ClaimedCount        int              `json:"claimed_count"`
	CompletedCount      int              `json:"completed_count"`
	WorkflowPath        string           `json:"workflow_path,omitempty"`
	WorkflowModTime     *time.Time       `json:"workflow_mod_time,omitempty"`
	CodexTotals         statusTokenInfo  `json:"codex_totals"`
	CodexRateLimits     any              `json:"codex_rate_limits,omitempty"`
}

func main() {
	var cfg serverConfig
	flag.StringVar(&cfg.Addr, "addr", defaultAddr, "Local address for the dashboard server")
	flag.StringVar(&cfg.StatusFile, "status-file", "", "Path to one orchestrator JSON status snapshot; overrides --status-dir")
	flag.StringVar(&cfg.StatusDir, "status-dir", defaultStatusDir, "Directory of orchestrator JSON status snapshots for fleet view")
	flag.Parse()

	if cfg.StatusFile == "" && cfg.StatusDir == "" {
		fmt.Fprintln(os.Stderr, "either --status-file or --status-dir is required")
		os.Exit(2)
	}
	if cfg.Addr == "" {
		fmt.Fprintln(os.Stderr, "--addr cannot be empty")
		os.Exit(2)
	}

	log.Printf("serving Symphony dashboard at http://%s/ watching %s", cfg.Addr, cfg.watchDescription())
	if err := http.ListenAndServe(cfg.Addr, newHandler(cfg)); err != nil {
		log.Fatal(err)
	}
}

func (cfg serverConfig) watchDescription() string {
	if cfg.StatusFile != "" {
		return cfg.StatusFile
	}
	return filepath.Join(cfg.StatusDir, "*.json")
}

func newHandler(cfg serverConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/status", cfg.statusHandler)
	mux.HandleFunc("/meta", cfg.metaHandler)
	return localHeaders(mux)
}

func localHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dashboardHTML))
}

func (cfg serverConfig) metaHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/meta" {
		http.NotFound(w, r)
		return
	}
	meta := metaResponse{SourceMode: "directory", StatusDir: cfg.StatusDir, ServedAt: time.Now().UTC()}
	if cfg.StatusFile != "" {
		meta.SourceMode = "file"
		meta.StatusFile = cfg.StatusFile
		meta.StatusDir = ""
	}
	writeJSON(w, http.StatusOK, meta)
}

func (cfg serverConfig) statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/status" {
		http.NotFound(w, r)
		return
	}

	var (
		data []byte
		err  error
	)
	if cfg.StatusFile != "" {
		data, err = readStatusFile(cfg.StatusFile)
	} else {
		data, err = readStatusDir(cfg.StatusDir)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: err.Error()})
			return
		}
		if strings.Contains(err.Error(), "not valid JSON") {
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func readStatusFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("status file %s does not exist yet: %w", path, err)
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("status file %s is not valid JSON", path)
	}
	return data, nil
}

func readStatusDir(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("status directory %s does not exist yet: %w", dir, err)
		}
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.Contains(name, ".tmp-") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("status directory %s has no JSON status snapshots: %w", dir, os.ErrNotExist)
	}

	agg := aggregateSnapshot{
		GeneratedAt:  time.Now().UTC(),
		SourceMode:   "directory",
		Running:      []map[string]any{},
		Retrying:     []map[string]any{},
		NeedsHuman:   []map[string]any{},
		RecentRuns:   []map[string]any{},
		Sources:      []sourceSummary{},
		SourceErrors: []sourceSummary{},
	}
	runners := map[string]bool{}
	linearModes := map[string]bool{}
	workflowPaths := map[string]bool{}
	var latestRateLimitAt time.Time

	for _, path := range files {
		info, statErr := os.Stat(path)
		summary := sourceSummary{Path: path, File: filepath.Base(path)}
		if statErr == nil {
			summary.ModTime = info.ModTime()
		}

		data, err := readStatusFile(path)
		if err != nil {
			summary.Error = err.Error()
			agg.SourceErrors = append(agg.SourceErrors, summary)
			continue
		}

		var snap statusSnapshot
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&snap); err != nil {
			summary.Error = err.Error()
			agg.SourceErrors = append(agg.SourceErrors, summary)
			continue
		}

		summary.GeneratedAt = snap.GeneratedAt
		summary.RunningCount = len(snap.Running)
		summary.RetryingCount = len(snap.Retrying)
		summary.HumanCount = len(snap.NeedsHuman)
		summary.RecentCount = len(snap.RecentRuns)
		agg.Sources = append(agg.Sources, summary)

		appendRows := func(target *[]map[string]any, rows []map[string]any) {
			for _, row := range rows {
				annotated := cloneMap(row)
				annotated["_source"] = path
				annotated["_source_file"] = filepath.Base(path)
				if snap.GeneratedAt != nil {
					annotated["_source_generated_at"] = snap.GeneratedAt.Format(time.RFC3339Nano)
				}
				*target = append(*target, annotated)
			}
		}
		appendRows(&agg.Running, snap.Running)
		appendRows(&agg.Retrying, snap.Retrying)
		appendRows(&agg.NeedsHuman, snap.NeedsHuman)
		appendRows(&agg.RecentRuns, snap.RecentRuns)

		if snap.PollIntervalMs > 0 && (agg.PollIntervalMs == 0 || snap.PollIntervalMs < agg.PollIntervalMs) {
			agg.PollIntervalMs = snap.PollIntervalMs
		}
		agg.MaxConcurrentAgents += snap.MaxConcurrentAgents
		agg.ClaimedCount += snap.ClaimedCount
		agg.CompletedCount += snap.CompletedCount
		agg.CodexTotals.InputTokens += snap.CodexTotals.InputTokens
		agg.CodexTotals.OutputTokens += snap.CodexTotals.OutputTokens
		agg.CodexTotals.TotalTokens += snap.CodexTotals.TotalTokens
		agg.CodexTotals.SecondsRunning += snap.CodexTotals.SecondsRunning
		if snap.CodexRateLimits != nil && ((snap.GeneratedAt == nil && latestRateLimitAt.IsZero()) || (snap.GeneratedAt != nil && (latestRateLimitAt.IsZero() || snap.GeneratedAt.After(latestRateLimitAt)))) {
			agg.CodexRateLimits = snap.CodexRateLimits
			if snap.GeneratedAt != nil {
				latestRateLimitAt = *snap.GeneratedAt
			}
		}
		if snap.WorkflowModTime != nil && (agg.WorkflowModTime == nil || snap.WorkflowModTime.After(*agg.WorkflowModTime)) {
			t := *snap.WorkflowModTime
			agg.WorkflowModTime = &t
		}
		if snap.AgentRunner != "" {
			runners[snap.AgentRunner] = true
		}
		if snap.LinearAccess != "" {
			linearModes[snap.LinearAccess] = true
		}
		if snap.WorkflowPath != "" {
			workflowPaths[snap.WorkflowPath] = true
		}
	}

	if len(agg.Sources) == 0 && len(agg.SourceErrors) > 0 {
		return nil, fmt.Errorf("status directory %s has no readable JSON status snapshots", dir)
	}
	agg.SourceCount = len(agg.Sources)
	agg.AgentRunner = summarizeStrings(runners)
	agg.LinearAccess = summarizeStrings(linearModes)
	agg.WorkflowPath = summarizeStrings(workflowPaths)
	sort.Slice(agg.RecentRuns, func(i, j int) bool {
		return rowTime(agg.RecentRuns[i], "finished_at").After(rowTime(agg.RecentRuns[j], "finished_at"))
	})
	sort.Slice(agg.Running, func(i, j int) bool {
		return rowString(agg.Running[i], "identifier") < rowString(agg.Running[j], "identifier")
	})

	return json.MarshalIndent(agg, "", "  ")
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func summarizeStrings(values map[string]bool) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		for value := range values {
			return value
		}
	}
	return "multiple"
}

func rowString(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return value
}

func rowTime(row map[string]any, key string) time.Time {
	value, _ := row[key].(string)
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Symphony Harness Status</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #eef2f5;
      --surface: #ffffff;
      --surface-alt: #f7f9fb;
      --line: #d5dde5;
      --line-strong: #aebbc8;
      --text: #121a24;
      --muted: #5b6c7e;
      --green: #0f7a4c;
      --green-bg: #e8f5ee;
      --red: #b3261e;
      --red-bg: #fdebea;
      --amber: #8a5a00;
      --amber-bg: #fff4d6;
      --blue: #075da8;
      --blue-bg: #e7f0fb;
      --shadow: 0 1px 2px rgba(15, 23, 42, 0.07);
      --shadow-elevated: 0 10px 24px rgba(15, 23, 42, 0.08);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      line-height: 1.4;
      letter-spacing: 0;
    }

    * { box-sizing: border-box; }

    body {
      margin: 0;
      min-width: 320px;
      background: var(--bg);
      color: var(--text);
    }

    header {
      border-bottom: 1px solid var(--line);
      background: var(--surface);
      box-shadow: 0 1px 0 rgba(15, 23, 42, 0.04);
    }

    .header-inner {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 16px;
      align-items: center;
      width: min(1440px, 100%);
      margin: 0 auto;
      padding: 18px 24px;
    }

    h1 {
      margin: 0;
      font-size: 21px;
      font-weight: 700;
    }

    .subline {
      margin-top: 5px;
      color: var(--muted);
      font-size: 13px;
      overflow-wrap: anywhere;
    }

    .controls {
      display: flex;
      flex-wrap: wrap;
      justify-content: flex-end;
      gap: 8px;
      min-width: 220px;
    }

    button, select {
      height: 34px;
      border: 1px solid var(--line-strong);
      border-radius: 6px;
      background: var(--surface);
      color: var(--text);
      font: inherit;
      font-size: 13px;
      box-shadow: var(--shadow);
    }

    button {
      padding: 0 12px;
      cursor: pointer;
    }

    button:hover, select:hover {
      border-color: var(--blue);
      color: var(--blue);
    }

    select {
      padding: 0 8px;
    }

    main {
      width: min(1440px, 100%);
      margin: 0 auto;
      padding: 20px 24px 32px;
    }

    .banner {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      align-items: center;
      margin-bottom: 16px;
      padding: 11px 13px;
      border: 1px solid var(--line);
      border-left: 4px solid var(--line-strong);
      border-radius: 6px;
      background: var(--surface);
      box-shadow: var(--shadow);
      color: var(--muted);
      font-size: 13px;
    }

    .banner strong { color: var(--text); }
    .banner .status-dot {
      width: 9px;
      height: 9px;
      border-radius: 50%;
      background: var(--line-strong);
      flex: 0 0 auto;
    }
    .banner.ok .status-dot { background: var(--green); }
    .banner.warn .status-dot { background: var(--amber); }
    .banner.error .status-dot { background: var(--red); }
    .banner.ok { border-left-color: var(--green); }
    .banner.warn { border-left-color: var(--amber); }
    .banner.error { border-left-color: var(--red); }

    .stats {
      display: grid;
      grid-template-columns: repeat(6, minmax(140px, 1fr));
      gap: 10px;
      margin-bottom: 16px;
    }

    .stat {
      position: relative;
      overflow: hidden;
      min-height: 78px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: var(--surface);
      padding: 13px 12px 12px 14px;
      box-shadow: var(--shadow);
    }

    .stat::before {
      content: "";
      position: absolute;
      inset: 0 auto 0 0;
      width: 4px;
      background: var(--line-strong);
    }

    .stat-label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0;
    }

    .stat-value {
      margin-top: 6px;
      font-size: 25px;
      font-weight: 700;
      overflow-wrap: anywhere;
    }

    .stat-detail {
      margin-top: 2px;
      color: var(--muted);
      font-size: 12px;
      overflow-wrap: anywhere;
    }

    .stat.green { border-color: #a7d8bd; background: var(--green-bg); }
    .stat.green::before { background: var(--green); }
    .stat.red { border-color: #f4b4ae; background: var(--red-bg); }
    .stat.red::before { background: var(--red); }
    .stat.amber { border-color: #f0d086; background: var(--amber-bg); }
    .stat.amber::before { background: var(--amber); }
    .stat.blue { border-color: #b2cdec; background: var(--blue-bg); }
    .stat.blue::before { background: var(--blue); }

    .grid {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      gap: 16px;
    }

    section {
      border: 1px solid var(--line);
      border-radius: 6px;
      background: var(--surface);
      box-shadow: var(--shadow);
      overflow: hidden;
    }

    .section-head {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
      padding: 12px 14px;
      border-bottom: 1px solid var(--line);
      background: var(--surface-alt);
    }

    .section-head.toggle-head {
      cursor: pointer;
      user-select: none;
    }

    .section-head.toggle-head:hover {
      background: #eef3f7;
    }

    .head-actions {
      display: inline-flex;
      align-items: center;
      justify-content: flex-end;
      gap: 10px;
      flex-wrap: wrap;
    }

    .disclosure {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 16px;
      height: 16px;
      color: var(--muted);
      font-size: 14px;
      font-weight: 700;
      line-height: 1;
    }

    .title-row {
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }

    h2 {
      margin: 0;
      font-size: 15px;
      font-weight: 700;
      color: var(--text);
    }

    .count {
      color: var(--muted);
      font-size: 12px;
      text-align: right;
      overflow-wrap: anywhere;
    }

    .table-wrap {
      width: 100%;
      overflow-x: auto;
    }

    .table-wrap.tight {
      max-height: 230px;
      overflow-y: auto;
    }

    table {
      width: 100%;
      min-width: 860px;
      border-collapse: collapse;
      table-layout: fixed;
    }

    th, td {
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      text-align: left;
      vertical-align: top;
      font-size: 13px;
      overflow-wrap: anywhere;
    }

    th {
      color: var(--muted);
      background: #fbfcfd;
      font-size: 12px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0;
    }

    .tight th {
      position: sticky;
      top: 0;
      z-index: 1;
    }

    tr:last-child td { border-bottom: 0; }
    tr.clickable { cursor: pointer; }
    tr.clickable:hover td { background: #f3f8fe; }
    tr.selected td { background: var(--amber-bg); }

    .mono {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;
      font-size: 12px;
    }

    .pill {
      display: inline-flex;
      align-items: center;
      max-width: 100%;
      min-height: 22px;
      padding: 2px 8px;
      border: 1px solid var(--line-strong);
      border-radius: 999px;
      background: var(--surface);
      color: var(--text);
      font-size: 12px;
      font-weight: 600;
      overflow-wrap: anywhere;
    }

    .pill.ok { border-color: #a7d8bd; color: var(--green); background: var(--green-bg); }
    .pill.warn { border-color: #f0d086; color: var(--amber); background: var(--amber-bg); }
    .pill.error { border-color: #f4b4ae; color: var(--red); background: var(--red-bg); }
    .pill.blue { border-color: #b2cdec; color: var(--blue); background: var(--blue-bg); }

    .empty {
      padding: 18px 14px;
      color: var(--muted);
      font-size: 13px;
    }

    .kv {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 1px;
      background: var(--line);
    }

    .config-drawer {
      max-height: 0;
      overflow: hidden;
      transition: max-height 180ms ease;
      border-top: 0 solid var(--line);
    }

    .config-drawer.open {
      max-height: 520px;
      border-top-width: 1px;
    }

    .kv-item {
      min-height: 62px;
      padding: 12px;
      background: var(--surface);
      overflow-wrap: anywhere;
    }

    .kv-key {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
    }

    .kv-key-row {
      display: flex;
      align-items: center;
      gap: 6px;
      min-width: 0;
    }

    .info {
      display: inline-grid;
      place-items: center;
      width: 16px;
      height: 16px;
      border: 1px solid var(--line-strong);
      border-radius: 50%;
      color: var(--muted);
      background: var(--surface);
      cursor: default;
      font-size: 11px;
      font-weight: 700;
      line-height: 1;
      flex: 0 0 auto;
      text-transform: none;
    }

    .tooltip {
      position: fixed;
      z-index: 20;
      max-width: min(320px, calc(100vw - 24px));
      padding: 8px 10px;
      border: 1px solid var(--line-strong);
      border-radius: 6px;
      background: var(--text);
      color: var(--surface);
      box-shadow: var(--shadow-elevated);
      font-size: 12px;
      line-height: 1.35;
      pointer-events: none;
      opacity: 0;
      transform: translateY(2px);
      transition: opacity 80ms ease, transform 80ms ease;
    }

    .tooltip.visible {
      opacity: 1;
      transform: translateY(0);
    }

    .kv-value {
      margin-top: 4px;
      font-size: 13px;
    }

    .detail-body {
      display: grid;
      grid-template-columns: minmax(240px, 360px) minmax(0, 1fr);
      gap: 1px;
      background: var(--line);
    }

    .detail-summary, .detail-json {
      background: var(--surface);
      padding: 12px;
      min-width: 0;
    }

    pre {
      margin: 0;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
      font-size: 12px;
    }

    @media (max-width: 980px) {
      .header-inner {
        grid-template-columns: 1fr;
      }
      .controls {
        justify-content: flex-start;
      }
      .stats {
        grid-template-columns: repeat(2, minmax(140px, 1fr));
      }
      .kv {
        grid-template-columns: repeat(2, minmax(0, 1fr));
      }
      .detail-body {
        grid-template-columns: 1fr;
      }
    }

    @media (max-width: 560px) {
      .header-inner, main {
        padding-left: 14px;
        padding-right: 14px;
      }
      .stats {
        grid-template-columns: 1fr;
      }
      .kv {
        grid-template-columns: 1fr;
      }
      button, select {
        width: 100%;
      }
      .controls {
        width: 100%;
      }
      .section-head {
        align-items: flex-start;
        flex-direction: column;
      }
      .head-actions {
        justify-content: flex-start;
        width: 100%;
      }
      .count {
        text-align: left;
      }
    }
  </style>
</head>
<body>
  <header>
    <div class="header-inner">
      <div>
        <h1>Symphony Harness Status</h1>
        <div class="subline">Watching <span id="source" class="mono">status file</span></div>
      </div>
      <div class="controls" aria-label="Dashboard controls">
        <button id="refresh" type="button">Refresh</button>
        <select id="interval" aria-label="Auto refresh interval">
          <option value="0">Auto refresh off</option>
          <option value="2000">Every 2 seconds</option>
          <option value="5000" selected>Every 5 seconds</option>
          <option value="10000">Every 10 seconds</option>
        </select>
      </div>
    </div>
  </header>

  <main>
    <div id="banner" class="banner warn" role="status">
      <span class="status-dot" aria-hidden="true"></span>
      <strong id="banner-title">Waiting for status</strong>
      <span id="banner-detail">No snapshot has been loaded yet.</span>
    </div>

    <div id="stats" class="stats" aria-label="Harness summary"></div>

    <div class="grid">
      <section aria-labelledby="config-title">
        <div id="config-head" class="section-head toggle-head" role="button" tabindex="0" aria-expanded="false" aria-controls="config-drawer">
          <div class="title-row">
            <span id="config-toggle" class="disclosure" aria-hidden="true">&gt;</span>
            <h2 id="config-title">Runtime Configuration</h2>
          </div>
          <div class="head-actions">
            <span id="generated" class="count">Generated unknown</span>
          </div>
        </div>
        <div id="config-drawer" class="config-drawer">
          <div id="config" class="kv"></div>
        </div>
      </section>

      <section aria-labelledby="running-title">
        <div class="section-head">
          <h2 id="running-title">Running Agents</h2>
          <span id="running-count" class="count">0 running</span>
        </div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th style="width: 110px;">Issue</th>
                <th style="width: 150px;">Source</th>
                <th style="width: 120px;">State</th>
                <th style="width: 90px;">Attempt</th>
                <th style="width: 120px;">Runtime</th>
                <th style="width: 170px;">Last Event</th>
                <th style="width: 170px;">Tokens</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody id="running-body"></tbody>
          </table>
        </div>
        <div id="running-empty" class="empty">No running agents.</div>
      </section>

      <section aria-labelledby="attention-title">
        <div class="section-head">
          <h2 id="attention-title">Retries And Human Attention</h2>
          <span id="attention-count" class="count">0 queued</span>
        </div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th style="width: 110px;">Issue</th>
                <th style="width: 150px;">Source</th>
                <th style="width: 120px;">Queue</th>
                <th style="width: 100px;">Attempt</th>
                <th style="width: 180px;">Due Or Since</th>
                <th>Reason</th>
              </tr>
            </thead>
            <tbody id="attention-body"></tbody>
          </table>
        </div>
        <div id="attention-empty" class="empty">No retries or human escalations.</div>
      </section>

      <section aria-labelledby="recent-title">
        <div class="section-head">
          <h2 id="recent-title">Recent Runs</h2>
          <span id="recent-count" class="count">0 runs</span>
        </div>
        <div class="table-wrap tight">
          <table>
            <thead>
              <tr>
                <th style="width: 110px;">Issue</th>
                <th style="width: 150px;">Source</th>
                <th style="width: 115px;">Status</th>
                <th style="width: 90px;">Attempt</th>
                <th style="width: 120px;">Duration</th>
                <th style="width: 170px;">Tokens</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody id="recent-body"></tbody>
          </table>
        </div>
        <div id="recent-empty" class="empty">No completed runs in the snapshot.</div>
      </section>

      <section id="detail" aria-labelledby="detail-title" hidden>
        <div class="section-head">
          <h2 id="detail-title">Run Detail</h2>
          <span id="detail-subtitle" class="count">Select a row</span>
        </div>
        <div class="detail-body">
          <div id="detail-summary" class="detail-summary"></div>
          <div class="detail-json">
            <pre id="detail-json"></pre>
          </div>
        </div>
      </section>
    </div>
  </main>

  <div id="tooltip" class="tooltip" role="tooltip" aria-hidden="true"></div>

  <script>
    const $ = (id) => document.getElementById(id);
    let refreshTimer = null;
    let configExpanded = false;
    let tooltipTimer = null;

    const configHelp = {
      "Poll Interval": "How often the orchestrator polls Linear for eligible work.",
      "Max Agents": "Configured maximum concurrent agent slots represented by the loaded status snapshots.",
      "Runner": "Coding-agent backend used by the harness.",
      "Linear Access": "How agents can access Linear. host_proxy means the host keeps the token and exposes a constrained tool.",
      "Source Mode": "Whether the dashboard is reading one status file or aggregating a status directory.",
      "Sources": "Number of readable status snapshots currently included in this dashboard view.",
      "Workflow": "Workflow config path represented by the snapshots. multiple means the loaded snapshots point at different paths.",
      "Workflow Updated": "Filesystem modification time of the newest workflow file represented by the snapshots.",
      "Claimed": "Issues currently claimed by the scheduler in the loaded snapshots.",
      "Completed": "Completed run count retained in the loaded snapshots.",
      "Source Errors": "Status files in the watched directory that could not be read or parsed."
    };

    function text(value, fallback) {
      if (value === null || value === undefined || value === "") {
        return fallback === undefined ? "none" : fallback;
      }
      return String(value);
    }

    function number(value) {
      return Number.isFinite(Number(value)) ? Number(value) : 0;
    }

    function plural(count, singular, pluralText) {
      return count === 1 ? count + " " + singular : count + " " + (pluralText || singular + "s");
    }

    function fmtTime(value) {
      if (!value) return "unknown";
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return "unknown";
      return date.toLocaleString();
    }

    function fmtDuration(ms) {
      const total = Math.max(0, Math.round(number(ms) / 1000));
      const hours = Math.floor(total / 3600);
      const mins = Math.floor((total % 3600) / 60);
      const secs = total % 60;
      if (hours > 0) return hours + "h " + mins + "m";
      if (mins > 0) return mins + "m " + secs + "s";
      return secs + "s";
    }

    function fmtTokens(info) {
      if (!info) return "none";
      const total = number(info.total_tokens);
      const input = number(info.input_tokens);
      const output = number(info.output_tokens);
      const seconds = number(info.seconds_running);
      const pieces = [total.toLocaleString() + " total", input.toLocaleString() + " in", output.toLocaleString() + " out"];
      if (seconds > 0) pieces.push(fmtDuration(seconds * 1000));
      return pieces.join(" / ");
    }

    function fmtRateLimits(value) {
      if (!value || typeof value !== "object") return "none";
      const parts = [];
      if (value.limit_name) parts.push(value.limit_name);
      if (value.plan_type) parts.push(value.plan_type);
      if (value.primary && Number.isFinite(Number(value.primary.used_percent))) {
        parts.push("primary " + value.primary.used_percent + "%");
      }
      if (value.secondary && Number.isFinite(Number(value.secondary.used_percent))) {
        parts.push("secondary " + value.secondary.used_percent + "%");
      }
      if (value.credits) {
        if (value.credits.unlimited) parts.push("credits unlimited");
        else if (value.credits.balance) parts.push("credits " + value.credits.balance);
      }
      if (value.rate_limit_reached_type) parts.push("reached " + value.rate_limit_reached_type);
      if (parts.length > 0) return parts.join(" / ");
      if (value.limit_id) return value.limit_id;
      return JSON.stringify(value);
    }

    function statusTone(status) {
      const s = String(status || "").toLowerCase();
      if (s.includes("success") || s.includes("completed") || s === "done") return "ok";
      if (s.includes("fail") || s.includes("error") || s.includes("timeout") || s.includes("stall")) return "error";
      if (s.includes("cancel") || s.includes("retry") || s.includes("human")) return "warn";
      return "blue";
    }

    function setBanner(kind, title, detail) {
      const banner = $("banner");
      banner.className = "banner " + kind;
      $("banner-title").textContent = title;
      $("banner-detail").textContent = detail;
    }

    function clearNode(node) {
      while (node.firstChild) node.removeChild(node.firstChild);
    }

    function stat(label, value, detail, tone) {
      const node = document.createElement("div");
      node.className = "stat" + (tone ? " " + tone : "");
      const labelNode = document.createElement("div");
      labelNode.className = "stat-label";
      labelNode.textContent = label;
      const valueNode = document.createElement("div");
      valueNode.className = "stat-value";
      valueNode.textContent = value;
      const detailNode = document.createElement("div");
      detailNode.className = "stat-detail";
      detailNode.textContent = detail;
      node.append(labelNode, valueNode, detailNode);
      return node;
    }

    function renderStats(snapshot) {
      const stats = $("stats");
      clearNode(stats);
      const running = (snapshot.running || []).length;
      const retrying = (snapshot.retrying || []).length;
      const human = (snapshot.needs_human || []).length;
      const recent = (snapshot.recent_runs || []).length;
      const sources = number(snapshot.source_count || (snapshot.sources || []).length || 1);
      const runningDetail = snapshot.source_mode === "directory" ? "across " + plural(sources, "source") : plural(snapshot.max_concurrent_agents || 0, "slot");
      stats.append(
        stat("Running", running.toString(), runningDetail, running > 0 ? "blue" : ""),
        stat("Retries", retrying.toString(), "waiting for backoff", retrying > 0 ? "amber" : ""),
        stat("Needs Human", human.toString(), "escalated by harness", human > 0 ? "red" : ""),
        stat("Recent Runs", recent.toString(), "kept in memory", ""),
        stat("Sources", sources.toString(), snapshot.source_mode === "directory" ? "status files" : "snapshot file", ""),
        stat("Codex Tokens", number((snapshot.codex_totals || {}).total_tokens).toLocaleString(), fmtTokens(snapshot.codex_totals), "green")
      );
    }

    function setConfigOpen(open) {
      configExpanded = open;
      $("config-drawer").classList.toggle("open", open);
      $("config-head").setAttribute("aria-expanded", String(open));
      $("config-toggle").textContent = open ? "v" : ">";
    }

    function showTooltip(target, message) {
      const tooltip = $("tooltip");
      tooltip.textContent = message;
      tooltip.setAttribute("aria-hidden", "false");
      const rect = target.getBoundingClientRect();
      const margin = 8;
      tooltip.style.left = margin + "px";
      tooltip.style.top = margin + "px";
      tooltip.classList.add("visible");
      const tipRect = tooltip.getBoundingClientRect();
      let left = rect.left;
      let top = rect.bottom + margin;
      if (left + tipRect.width > window.innerWidth - margin) {
        left = window.innerWidth - tipRect.width - margin;
      }
      if (top + tipRect.height > window.innerHeight - margin) {
        top = rect.top - tipRect.height - margin;
      }
      tooltip.style.left = Math.max(margin, left) + "px";
      tooltip.style.top = Math.max(margin, top) + "px";
    }

    function hideTooltip() {
      window.clearTimeout(tooltipTimer);
      const tooltip = $("tooltip");
      tooltip.classList.remove("visible");
      tooltip.setAttribute("aria-hidden", "true");
    }

    function kv(key, value, help) {
      const node = document.createElement("div");
      node.className = "kv-item";
      const keyRow = document.createElement("div");
      keyRow.className = "kv-key-row";
      const keyNode = document.createElement("div");
      keyNode.className = "kv-key";
      keyNode.textContent = key;
      keyRow.appendChild(keyNode);
      if (help) {
        const info = document.createElement("span");
        info.className = "info";
        info.textContent = "i";
        info.setAttribute("aria-label", help);
        info.addEventListener("mouseenter", () => {
          window.clearTimeout(tooltipTimer);
          tooltipTimer = window.setTimeout(() => showTooltip(info, help), 250);
        });
        info.addEventListener("mouseleave", hideTooltip);
        info.addEventListener("focus", () => showTooltip(info, help));
        info.addEventListener("blur", hideTooltip);
        keyRow.appendChild(info);
      }
      const valueNode = document.createElement("div");
      valueNode.className = "kv-value";
      valueNode.textContent = value;
      node.append(keyRow, valueNode);
      return node;
    }

    function renderConfig(snapshot) {
      const config = $("config");
      clearNode(config);
      config.append(
        kv("Poll Interval", fmtDuration(snapshot.poll_interval_ms), configHelp["Poll Interval"]),
        kv("Max Agents", text(snapshot.max_concurrent_agents, "0"), configHelp["Max Agents"]),
        kv("Runner", text(snapshot.agent_runner, "unknown"), configHelp["Runner"]),
        kv("Linear Access", text(snapshot.linear_access, "unknown"), configHelp["Linear Access"]),
        kv("Source Mode", text(snapshot.source_mode, "file"), configHelp["Source Mode"]),
        kv("Sources", text(snapshot.source_count || (snapshot.sources || []).length || 1, "1"), configHelp["Sources"]),
        kv("Workflow", text(snapshot.workflow_path, "none"), configHelp["Workflow"]),
        kv("Workflow Updated", fmtTime(snapshot.workflow_mod_time), configHelp["Workflow Updated"]),
        kv("Claimed", text(snapshot.claimed_count, "0"), configHelp["Claimed"]),
        kv("Completed", text(snapshot.completed_count, "0"), configHelp["Completed"]),
        kv("Source Errors", text((snapshot.source_errors || []).length, "0"), configHelp["Source Errors"])
      );
      $("generated").textContent = "Generated " + fmtTime(snapshot.generated_at);
    }

    function pill(value, tone) {
      const span = document.createElement("span");
      span.className = "pill " + (tone || "");
      span.textContent = text(value, "none");
      return span;
    }

    function sessionText(row) {
      const parts = [];
      if (row.session_id) parts.push("session " + row.session_id);
      if (row.thread_id) parts.push("thread " + row.thread_id);
      if (row.turn_id) parts.push("turn " + row.turn_id);
      return parts.length ? parts.join(" / ") : "none";
    }

    function addCell(tr, value, className) {
      const td = document.createElement("td");
      if (className) td.className = className;
      if (value instanceof Node) {
        td.appendChild(value);
      } else {
        td.textContent = text(value, "");
      }
      tr.appendChild(td);
    }

    function setEmpty(id, isEmpty) {
      $(id).style.display = isEmpty ? "block" : "none";
    }

    function sourceLabel(row) {
      return text(row._source_file || row._source, "current snapshot");
    }

    function showDetail(kind, row) {
      $("detail").hidden = false;
      $("detail-title").textContent = kind + " Detail";
      $("detail-subtitle").textContent = text(row.identifier || row.issue_id, "unknown issue") + " from " + sourceLabel(row);
      const summary = $("detail-summary");
      clearNode(summary);
      summary.append(
        kv("Issue", text(row.identifier || row.issue_id, "unknown")),
        kv("Source", sourceLabel(row)),
        kv("Session", sessionText(row)),
        kv("Generated", fmtTime(row._source_generated_at)),
        kv("Tokens", fmtTokens(row.token_info)),
        kv("Rate Limit", fmtRateLimits(row.rate_limits))
      );
      $("detail-json").textContent = JSON.stringify(row, null, 2);
    }

    function makeClickable(tr, kind, row) {
      tr.className = "clickable";
      tr.tabIndex = 0;
      tr.addEventListener("click", () => showDetail(kind, row));
      tr.addEventListener("keydown", (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          showDetail(kind, row);
        }
      });
    }

    function renderRunning(rows) {
      const body = $("running-body");
      clearNode(body);
      rows.forEach((row) => {
        const tr = document.createElement("tr");
        makeClickable(tr, "Running Agent", row);
        addCell(tr, row.identifier || row.issue_id, "mono");
        addCell(tr, sourceLabel(row), "mono");
        addCell(tr, pill(row.state, "blue"));
        addCell(tr, row.attempt === undefined || row.attempt === null ? "first" : row.attempt);
        addCell(tr, fmtDuration(row.duration_ms));
        addCell(tr, [text(row.last_event, "none"), fmtTime(row.last_timestamp)].filter(Boolean).join(" / "));
        addCell(tr, fmtTokens(row.token_info));
        addCell(tr, text(row.last_message, ""));
        body.appendChild(tr);
      });
      $("running-count").textContent = plural(rows.length, "running agent");
      setEmpty("running-empty", rows.length === 0);
    }

    function renderAttention(retrying, needsHuman) {
      const body = $("attention-body");
      clearNode(body);
      retrying.forEach((row) => {
        const tr = document.createElement("tr");
        makeClickable(tr, "Retry", row);
        addCell(tr, row.identifier || row.issue_id, "mono");
        addCell(tr, sourceLabel(row), "mono");
        addCell(tr, pill("Retrying", "warn"));
        addCell(tr, row.attempt);
        addCell(tr, row.due_at_ms ? new Date(row.due_at_ms).toLocaleString() : "unknown");
        addCell(tr, text(row.error, "waiting for retry window"));
        body.appendChild(tr);
      });
      needsHuman.forEach((row) => {
        const tr = document.createElement("tr");
        makeClickable(tr, "Needs Human", row);
        const reason = [text(row.reason, "needs human"), text(row.escalation_error, "")].filter(Boolean).join(" / ");
        addCell(tr, row.identifier || row.issue_id, "mono");
        addCell(tr, sourceLabel(row), "mono");
        addCell(tr, pill("Needs Human", "error"));
        addCell(tr, row.attempts);
        addCell(tr, fmtTime(row.since));
        addCell(tr, reason);
        body.appendChild(tr);
      });
      const total = retrying.length + needsHuman.length;
      $("attention-count").textContent = plural(total, "queued item");
      setEmpty("attention-empty", total === 0);
    }

    function renderRecent(rows) {
      const body = $("recent-body");
      clearNode(body);
      rows.forEach((row) => {
        const tr = document.createElement("tr");
        makeClickable(tr, "Recent Run", row);
        addCell(tr, row.identifier || row.issue_id, "mono");
        addCell(tr, sourceLabel(row), "mono");
        addCell(tr, pill(row.status, statusTone(row.status)));
        addCell(tr, row.attempt === undefined || row.attempt === null ? "first" : row.attempt);
        addCell(tr, fmtDuration(row.duration_ms));
        addCell(tr, fmtTokens(row.token_info));
        addCell(tr, text(row.error, ""));
        body.appendChild(tr);
      });
      $("recent-count").textContent = plural(rows.length, "run");
      setEmpty("recent-empty", rows.length === 0);
    }

    function render(snapshot, meta) {
      const watchPath = meta.source_mode === "directory"
        ? text(meta.status_dir, "status directory") + "/*.json"
        : text(meta.status_file, "status file");
      const sourceCount = snapshot.source_count || (snapshot.sources || []).length;
      $("source").textContent = watchPath + (sourceCount ? " (" + plural(sourceCount, "source") + ")" : "");
      renderStats(snapshot);
      renderConfig(snapshot);
      renderRunning(snapshot.running || []);
      renderAttention(snapshot.retrying || [], snapshot.needs_human || []);
      renderRecent(snapshot.recent_runs || []);
      const human = (snapshot.needs_human || []).length;
      const retrying = (snapshot.retrying || []).length;
      const running = (snapshot.running || []).length;
      if (human > 0) {
        setBanner("error", "Human attention required", plural(human, "issue") + " needs operator review.");
      } else if (retrying > 0) {
        setBanner("warn", "Retries queued", plural(retrying, "issue") + " waiting for backoff.");
      } else {
        setBanner("ok", "Fleet snapshot loaded", plural(running, "agent") + " currently running.");
      }
    }

    async function refresh() {
      try {
        setBanner("warn", "Refreshing", "Reading the current status snapshot.");
        const metaPromise = fetch("/meta", { cache: "no-store" }).then((r) => r.ok ? r.json() : {});
        const response = await fetch("/status", { cache: "no-store" });
        if (!response.ok) {
          const detail = await response.json().catch(() => ({ error: response.statusText }));
          throw new Error(detail.error || response.statusText);
        }
        const snapshot = await response.json();
        const meta = await metaPromise;
        render(snapshot, meta);
      } catch (err) {
        setBanner("error", "Status unavailable", err.message || String(err));
      }
    }

    function scheduleRefresh() {
      if (refreshTimer) window.clearInterval(refreshTimer);
      const interval = number($("interval").value);
      if (interval > 0) refreshTimer = window.setInterval(refresh, interval);
    }

    $("refresh").addEventListener("click", refresh);
    $("interval").addEventListener("change", scheduleRefresh);
    $("config-head").addEventListener("click", () => setConfigOpen(!configExpanded));
    $("config-head").addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        setConfigOpen(!configExpanded);
      }
    });
    setConfigOpen(false);
    scheduleRefresh();
    refresh();
  </script>
</body>
</html>
`
