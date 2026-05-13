// Package linear implements the Linear tracker adapter required by
// Symphony spec §11.
//
// Adapter responsibilities (per spec §11.1):
//  1. FetchCandidateIssues — issues in configured active states.
//  2. FetchIssuesByStates  — used for startup terminal cleanup.
//  3. FetchIssueStatesByIDs — used for active-run reconciliation.
//
// The adapter is a tracker reader, never a writer (spec §11.5). Ticket
// mutations (state transitions, comments) flow through the agent itself
// during a run, not through the orchestrator.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sasilver75/events/orchestrator/internal/domain"
)

// Error categories per Symphony spec §11.4.
var (
	ErrUnsupportedTrackerKind = errors.New("unsupported_tracker_kind")
	ErrMissingAPIKey          = errors.New("missing_tracker_api_key")
	ErrMissingProjectSlug     = errors.New("missing_tracker_project_slug")
	ErrLinearAPIRequest       = errors.New("linear_api_request")
	ErrLinearAPIStatus        = errors.New("linear_api_status")
	ErrLinearGraphQLErrors    = errors.New("linear_graphql_errors")
	ErrLinearUnknownPayload   = errors.New("linear_unknown_payload")
	ErrLinearMissingEndCursor = errors.New("linear_missing_end_cursor")
)

// defaultPageSize per spec §11.2 ("Page size default: 50").
const defaultPageSize = 50

// defaultRequestTimeout per spec §11.2 ("Network timeout: 30000 ms").
const defaultRequestTimeout = 30 * time.Second

// Client speaks Linear GraphQL. Configure once at orchestrator boot,
// reuse across all calls.
type Client struct {
	endpoint    string
	apiKey      string
	projectSlug string
	http        *http.Client
}

// Config carries the constructor inputs. Mirrors workflow.TrackerConfig
// but holds the resolved API key (not the env var name).
type Config struct {
	Endpoint    string // e.g. "https://api.linear.app/graphql"
	APIKey      string // resolved from env at boot
	ProjectSlug string
}

// New builds a Client. Validates required fields per spec §11.4 error
// categories. The http.Client uses a 30s timeout per spec §11.2.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.linear.app/graphql"
	}
	if cfg.APIKey == "" {
		return nil, ErrMissingAPIKey
	}
	if cfg.ProjectSlug == "" {
		return nil, ErrMissingProjectSlug
	}
	return &Client{
		endpoint:    cfg.Endpoint,
		apiKey:      cfg.APIKey,
		projectSlug: cfg.ProjectSlug,
		http:        &http.Client{Timeout: defaultRequestTimeout},
	}, nil
}

// FetchCandidateIssues returns all issues in the configured project whose
// state is in activeStates. Pages through Linear automatically. Symphony
// spec §11.1.1.
//
// Returns an empty slice (not nil) when there are no candidates — callers
// can range over the result either way.
func (c *Client) FetchCandidateIssues(ctx context.Context, activeStates []string) ([]domain.Issue, error) {
	var (
		issues []domain.Issue
		cursor *string
	)
	for {
		vars := map[string]any{
			"projectSlug": c.projectSlug,
			"states":      activeStates,
			"first":       defaultPageSize,
		}
		if cursor != nil {
			vars["after"] = *cursor
		}
		var page struct {
			Issues struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []rawIssue `json:"nodes"`
			} `json:"issues"`
		}
		if err := c.do(ctx, queryCandidateIssues, vars, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Issues.Nodes {
			issues = append(issues, r.normalize())
		}
		if !page.Issues.PageInfo.HasNextPage {
			break
		}
		if page.Issues.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("%w: hasNextPage=true but endCursor empty", ErrLinearMissingEndCursor)
		}
		cursor = &page.Issues.PageInfo.EndCursor
	}
	if issues == nil {
		issues = []domain.Issue{}
	}
	return issues, nil
}

// FetchIssueStatesByIDs returns minimal current state for each requested ID.
// Used by reconciliation in spec §8.5. Issues that no longer exist are
// silently omitted from the result map.
func (c *Client) FetchIssueStatesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	vars := map[string]any{"issueIds": ids}
	var resp struct {
		Issues struct {
			Nodes []struct {
				ID    string `json:"id"`
				State *struct {
					Name string `json:"name"`
				} `json:"state"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, queryIssueStatesByIDs, vars, &resp); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(resp.Issues.Nodes))
	for _, n := range resp.Issues.Nodes {
		if n.State == nil {
			continue
		}
		out[n.ID] = n.State.Name
	}
	return out, nil
}

// FetchIssuesByStates returns all issues currently in any of the given
// states. Used at startup for terminal-workspace cleanup (spec §8.6).
func (c *Client) FetchIssuesByStates(ctx context.Context, states []string) ([]domain.Issue, error) {
	vars := map[string]any{
		"projectSlug": c.projectSlug,
		"states":      states,
		"first":       250, // hard cap; if you have more terminal issues than this you have other problems
	}
	var resp struct {
		Issues struct {
			Nodes []rawIssue `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, queryTerminalIssues, vars, &resp); err != nil {
		return nil, err
	}
	out := make([]domain.Issue, 0, len(resp.Issues.Nodes))
	for _, r := range resp.Issues.Nodes {
		out = append(out, r.normalize())
	}
	return out, nil
}

// graphqlError is one entry in the top-level GraphQL `errors` array.
type graphqlError struct {
	Message string `json:"message"`
}

// do is the GraphQL transport. Centralizes header setup, status-code
// handling, and error-category mapping per spec §11.4.
//
// dst should be a pointer to a struct matching the `data` field shape
// of the response body.
func (c *Client) do(ctx context.Context, query string, vars map[string]any, dst any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": vars,
	})
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrLinearAPIRequest, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: new request: %v", ErrLinearAPIRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLinearAPIRequest, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrLinearAPIRequest, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: status %d: %s", ErrLinearAPIStatus, resp.StatusCode, truncate(string(raw), 200))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphqlError  `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: %v: %s", ErrLinearUnknownPayload, err, truncate(string(raw), 200))
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("%w: %v", ErrLinearGraphQLErrors, msgs)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("%w: data field missing or null", ErrLinearUnknownPayload)
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		return fmt.Errorf("%w: data unmarshal: %v", ErrLinearUnknownPayload, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
