package claudecode

import (
	"strings"
	"testing"
)

func TestNormalize_SessionStarted(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"system","subtype":"init","session_id":"sess-123","tools":[],"model":"claude-opus-4-7"}`)
	ev := normalize(line)
	if ev.Type != EventSessionStarted {
		t.Errorf("Type = %s, want session_started", ev.Type)
	}
	if ev.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.ThreadID != "sess-123" {
		t.Errorf("ThreadID = %q (should mirror SessionID)", ev.ThreadID)
	}
}

func TestNormalize_TurnCompleted(t *testing.T) {
	t.Parallel()
	line := []byte(`{
		"type":"result","subtype":"success","session_id":"sess-123",
		"is_error":false,"num_turns":3,
		"usage":{"input_tokens":1000,"output_tokens":500,"total_tokens":1500},
		"result":"final message"
	}`)
	ev := normalize(line)
	if ev.Type != EventTurnCompleted {
		t.Errorf("Type = %s, want turn_completed", ev.Type)
	}
	if ev.Usage.InputTokens != 1000 || ev.Usage.OutputTokens != 500 || ev.Usage.TotalTokens != 1500 {
		t.Errorf("Usage = %+v", ev.Usage)
	}
}

func TestNormalize_TurnFailed(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"result","subtype":"error_max_turns","session_id":"sess-123","is_error":true,"result":"max turns exceeded"}`)
	ev := normalize(line)
	if ev.Type != EventTurnFailed {
		t.Errorf("Type = %s, want turn_failed", ev.Type)
	}
	if ev.Error != "max turns exceeded" {
		t.Errorf("Error = %q", ev.Error)
	}
}

func TestNormalize_TurnCancelled(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"result","subtype":"cancelled","session_id":"sess-123","is_error":true}`)
	ev := normalize(line)
	if ev.Type != EventTurnCancelled {
		t.Errorf("Type = %s, want turn_cancelled", ev.Type)
	}
}

func TestNormalize_AssistantPassesThrough(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"assistant","message":{"id":"msg-1","content":[],"usage":{"input_tokens":100,"output_tokens":50}},"session_id":"sess-123"}`)
	ev := normalize(line)
	if ev.Type != EventOtherMessage {
		t.Errorf("Type = %s, want other_message", ev.Type)
	}
	if ev.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.TurnID != "msg-1" {
		t.Errorf("TurnID = %q", ev.TurnID)
	}
	if ev.Usage.TotalTokens != 150 {
		t.Errorf("Usage.TotalTokens = %d, want 150 (input+output)", ev.Usage.TotalTokens)
	}
}

func TestNormalize_Malformed(t *testing.T) {
	t.Parallel()
	line := []byte(`{not valid json`)
	ev := normalize(line)
	if ev.Type != EventMalformed {
		t.Errorf("Type = %s, want malformed", ev.Type)
	}
	if ev.Error == "" {
		t.Error("Error should be set on malformed")
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"foo", `'foo'`},
		{"don't", `'don'\''t'`},
		{"a b c", `'a b c'`},
		{"", `''`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildCommand_PromptViaBase64(t *testing.T) {
	t.Parallel()
	r := &Runner{
		Command:    "claude --print --output-format=stream-json",
		WorkingDir: "/Users/admin/events",
	}
	cmd := r.buildCommand("hello world", "")
	if !strings.Contains(cmd, "cd '/Users/admin/events'") {
		t.Errorf("missing cd: %s", cmd)
	}
	if !strings.Contains(cmd, "base64 -d") {
		t.Errorf("missing base64 decode: %s", cmd)
	}
	if !strings.Contains(cmd, "| claude --print --output-format=stream-json") {
		t.Errorf("missing claude invocation piped from base64: %s", cmd)
	}
}

func TestBuildCommand_WithResume(t *testing.T) {
	t.Parallel()
	r := &Runner{
		Command:    "claude --print --output-format=stream-json",
		WorkingDir: "/Users/admin/events",
	}
	cmd := r.buildCommand("continue", "sess-123")
	if !strings.Contains(cmd, "--resume 'sess-123'") {
		t.Errorf("missing --resume: %s", cmd)
	}
}
