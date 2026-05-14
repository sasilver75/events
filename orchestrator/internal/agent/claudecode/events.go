// Package claudecode runs Claude Code in headless stream-json mode inside
// a per-issue Tart VM, on behalf of the orchestrator. Symphony spec §10
// describes this contract for Codex App Server (JSON-RPC); we adapt it
// for Claude Code's JSON-stdout-per-line format.
//
// Mapping from Claude Code stream-json events → Symphony §10.4 events:
//
//	Claude Code              | Symphony §10.4
//	-------------------------+----------------------------------
//	{"type":"system",        | session_started (with session_id,
//	 "subtype":"init", ...}  |   model, tools)
//	{"type":"assistant",     | other_message (no spec-mandated
//	 ...}                    |   handling beyond pass-through)
//	{"type":"user",          | other_message
//	 "message":{...}}        |
//	{"type":"result",        | turn_completed (subtype=success) /
//	 "subtype":"success"     |   turn_failed (is_error=true) /
//	 or is_error:true ...}   |   turn_cancelled (subtype=cancelled)
//	(subprocess exit)        | startup_failed / port_exit
//
// The orchestrator only needs to know:
//   - When the session has a stable thread_id (so it can issue continuation
//     turns later)
//   - When the turn finished and how (success / failure / timeout)
//   - Updated token usage for telemetry
package claudecode

import (
	"encoding/json"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/agent"
)

type EventType = agent.EventType

const (
	EventSessionStarted = agent.EventSessionStarted
	EventStartupFailed  = agent.EventStartupFailed
	EventTurnCompleted  = agent.EventTurnCompleted
	EventTurnFailed     = agent.EventTurnFailed
	EventTurnCancelled  = agent.EventTurnCancelled
	EventTurnTimedOut   = agent.EventTurnTimedOut
	EventTurnStalled    = agent.EventTurnStalled
	EventOtherMessage   = agent.EventOtherMessage
	EventMalformed      = agent.EventMalformed
)

type Event = agent.Event
type Usage = agent.Usage

// rawClaudeEvent is the wire shape of one JSONL line from Claude Code's
// stream-json output. Fields not we touch are deliberately omitted; the
// raw bytes are preserved in Event.Raw for downstream logging.
type rawClaudeEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	IsError   *bool  `json:"is_error,omitempty"`
	Result    string `json:"result,omitempty"`
	NumTurns  int    `json:"num_turns,omitempty"`
	Usage     *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
	Message *struct {
		ID    string `json:"id"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage,omitempty"`
	} `json:"message,omitempty"`
}

// normalize converts one JSONL line to an Event.
func normalize(line []byte) Event {
	now := time.Now().UTC()
	var r rawClaudeEvent
	if err := json.Unmarshal(line, &r); err != nil {
		return Event{Type: EventMalformed, Timestamp: now, Raw: append([]byte(nil), line...), Error: err.Error()}
	}
	ev := Event{
		Timestamp: now,
		SessionID: r.SessionID,
		ThreadID:  r.SessionID, // Claude Code conflates these
		Raw:       append([]byte(nil), line...),
	}

	// Token usage: prefer top-level `usage` (present on `result`), fall
	// back to `message.usage` (present on assistant events).
	if r.Usage != nil {
		ev.Usage = Usage{
			InputTokens:  r.Usage.InputTokens,
			OutputTokens: r.Usage.OutputTokens,
			TotalTokens:  r.Usage.TotalTokens,
		}
		if ev.Usage.TotalTokens == 0 {
			ev.Usage.TotalTokens = ev.Usage.InputTokens + ev.Usage.OutputTokens
		}
	} else if r.Message != nil && r.Message.Usage != nil {
		ev.Usage = Usage{
			InputTokens:  r.Message.Usage.InputTokens,
			OutputTokens: r.Message.Usage.OutputTokens,
			TotalTokens:  r.Message.Usage.InputTokens + r.Message.Usage.OutputTokens,
		}
	}
	if r.Message != nil {
		ev.TurnID = r.Message.ID
	}

	switch r.Type {
	case "system":
		if r.Subtype == "init" || r.SessionID != "" {
			ev.Type = EventSessionStarted
			return ev
		}
		ev.Type = EventOtherMessage
		return ev
	case "result":
		// `result` is the terminal event. is_error || subtype="error_*"
		// → failed. subtype="cancelled" → cancelled. Otherwise success.
		if (r.IsError != nil && *r.IsError) || (r.Subtype != "" && r.Subtype != "success") {
			switch r.Subtype {
			case "cancelled":
				ev.Type = EventTurnCancelled
			default:
				ev.Type = EventTurnFailed
				ev.Error = r.Result
			}
			return ev
		}
		ev.Type = EventTurnCompleted
		return ev
	default:
		// assistant, user, error, anything else — pass through.
		ev.Type = EventOtherMessage
		return ev
	}
}
