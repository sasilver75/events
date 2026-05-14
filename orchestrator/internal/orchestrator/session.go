package orchestrator

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/domain"
)

const maxLiveMessageLength = 500

func mergeWorkerResultWithLiveSession(res WorkerResult, session domain.LiveSession) WorkerResult {
	if res.SessionID == "" {
		res.SessionID = session.SessionID
	}
	if res.ThreadID == "" {
		res.ThreadID = session.ThreadID
	}
	if res.TurnID == "" {
		res.TurnID = session.TurnID
	}
	if res.InputTokens == 0 {
		res.InputTokens = session.InputTokens
	}
	if res.OutputTokens == 0 {
		res.OutputTokens = session.OutputTokens
	}
	if res.TotalTokens == 0 {
		res.TotalTokens = session.TotalTokens
	}
	if res.RateLimits == nil {
		if rateLimits, ok := session.RateLimits.(*agent.RateLimitSnapshot); ok {
			res.RateLimits = rateLimits
		}
	}
	return res
}

func summarizeAgentEvent(ev agent.Event) string {
	if msg := strings.TrimSpace(ev.Message); msg != "" {
		return trim(msg, maxLiveMessageLength)
	}
	if msg := strings.TrimSpace(ev.Error); msg != "" {
		return trim(msg, maxLiveMessageLength)
	}
	if len(ev.Raw) == 0 {
		return ""
	}
	msg := strings.TrimSpace(summarizeRawAgentMessage(ev.Raw))
	if msg == "" {
		return ""
	}
	return trim(msg, maxLiveMessageLength)
}

func summarizeRawAgentMessage(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	return firstMessageString(value, 0)
}

func firstMessageString(value any, depth int) string {
	if depth > 6 {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		for _, item := range v {
			if msg := firstMessageString(item, depth+1); msg != "" {
				return msg
			}
		}
	case map[string]any:
		for _, key := range []string{"message", "text", "summary", "content", "title"} {
			if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		for _, item := range v {
			if msg := firstMessageString(item, depth+1); msg != "" {
				return msg
			}
		}
	}
	return ""
}
