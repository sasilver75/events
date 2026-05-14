package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sasilver75/events/orchestrator/internal/agent"
	"github.com/sasilver75/events/orchestrator/internal/domain"
)

func TestNew_RequiresAPIKey(t *testing.T) {
	t.Parallel()
	_, err := New(Config{ProjectSlug: "spur"})
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("err = %v, want ErrMissingAPIKey", err)
	}
}

func TestNew_RequiresProjectSlug(t *testing.T) {
	t.Parallel()
	_, err := New(Config{APIKey: "lin_api_xxx"})
	if !errors.Is(err, ErrMissingProjectSlug) {
		t.Errorf("err = %v, want ErrMissingProjectSlug", err)
	}
}

func TestFetchCandidateIssues_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_test" {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"issues": {
					"pageInfo": {"hasNextPage": false, "endCursor": ""},
					"nodes": [
						{
							"id": "uuid-1",
							"identifier": "SAM-12",
							"title": "Test ticket",
							"description": "Body",
							"priority": 2,
							"branchName": "sam-12-test-ticket",
							"url": "https://linear.app/samcorp/issue/SAM-12",
							"createdAt": "2026-05-10T12:00:00.000Z",
							"updatedAt": "2026-05-10T13:00:00.000Z",
							"assignee": {"id": "user-1", "name": "Agent", "email": "agent@example.com"},
							"state": {"name": "Ready"},
							"labels": {"nodes": [{"name": "AFK"}, {"name": "Feature"}]},
							"inverseRelations": {
								"nodes": [
									{"type": "blocks", "issue": {"id": "uuid-7", "identifier": "SAM-7", "state": {"name": "Done"}, "createdAt": "2026-05-01T00:00:00.000Z", "updatedAt": "2026-05-02T00:00:00.000Z"}},
									{"type": "related", "issue": {"id": "uuid-9", "identifier": "SAM-9", "state": {"name": "In Progress"}, "createdAt": "2026-05-01T00:00:00.000Z", "updatedAt": "2026-05-02T00:00:00.000Z"}}
								]
							}
						}
					]
				}
			}
		}`))
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	issues, err := c.FetchCandidateIssues(context.Background(), []string{"Ready", "In Progress"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	i := issues[0]
	if i.Identifier != "SAM-12" {
		t.Errorf("Identifier = %q", i.Identifier)
	}
	if i.State != "Ready" {
		t.Errorf("State = %q", i.State)
	}
	if i.AssigneeID != "user-1" {
		t.Errorf("AssigneeID = %q, want user-1", i.AssigneeID)
	}
	if len(i.Labels) != 2 || i.Labels[0] != "afk" || i.Labels[1] != "feature" {
		t.Errorf("Labels = %v (should be lowercased)", i.Labels)
	}
	if i.Priority == nil || *i.Priority != 2 {
		t.Errorf("Priority = %v, want 2", i.Priority)
	}
	if len(i.BlockedBy) != 1 || i.BlockedBy[0].Identifier != "SAM-7" {
		t.Errorf("BlockedBy = %v (should have just SAM-7, not the 'related' relation)", i.BlockedBy)
	}
	if i.BlockedBy[0].State != "Done" {
		t.Errorf("BlockedBy[0].State = %q", i.BlockedBy[0].State)
	}
}

func TestFetchCandidateIssues_EmptyResult(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	issues, err := c.FetchCandidateIssues(context.Background(), []string{"Ready"})
	if err != nil {
		t.Fatal(err)
	}
	if issues == nil {
		t.Error("issues should be empty slice, not nil")
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues, want 0", len(issues))
	}
}

func TestFetchCandidateIssues_GraphQLError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"bad project slug"}]}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	_, err := c.FetchCandidateIssues(context.Background(), []string{"Ready"})
	if !errors.Is(err, ErrLinearGraphQLErrors) {
		t.Errorf("err = %v, want ErrLinearGraphQLErrors", err)
	}
	if !strings.Contains(err.Error(), "bad project slug") {
		t.Errorf("err should surface the GraphQL message: %v", err)
	}
}

func TestFetchCandidateIssues_HTTPStatusError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	_, err := c.FetchCandidateIssues(context.Background(), []string{"Ready"})
	if !errors.Is(err, ErrLinearAPIStatus) {
		t.Errorf("err = %v, want ErrLinearAPIStatus", err)
	}
}

func TestFetchIssueStatesByIDs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[
			{"id":"uuid-1","identifier":"SAM-12","state":{"name":"In Progress"}},
			{"id":"uuid-2","identifier":"SAM-13","state":{"name":"Done"}}
		]}}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	out, err := c.FetchIssueStatesByIDs(context.Background(), []string{"uuid-1", "uuid-2", "uuid-3"})
	if err != nil {
		t.Fatal(err)
	}
	if out["uuid-1"] != "In Progress" {
		t.Errorf("uuid-1 = %q", out["uuid-1"])
	}
	if out["uuid-2"] != "Done" {
		t.Errorf("uuid-2 = %q", out["uuid-2"])
	}
	if _, present := out["uuid-3"]; present {
		t.Errorf("uuid-3 should be absent (Linear didn't return it)")
	}
}

func TestFetchIssuesByStates_Paginates(t *testing.T) {
	t.Parallel()
	var afterSeen []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !strings.Contains(req.Query, "TerminalIssues") {
			t.Fatalf("query = %s, want TerminalIssues", req.Query)
		}
		afterSeen = append(afterSeen, req.Variables["after"])
		w.Header().Set("Content-Type", "application/json")
		switch req.Variables["after"] {
		case nil:
			_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"},"nodes":[
				{"id":"uuid-1","identifier":"SAM-1","state":{"name":"Done"}}
			]}}}`))
		case "cursor-1":
			_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
				{"id":"uuid-2","identifier":"SAM-2","state":{"name":"Done"}}
			]}}}`))
		default:
			t.Fatalf("unexpected after cursor: %v", req.Variables["after"])
		}
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	issues, err := c.FetchIssuesByStates(context.Background(), []string{"Done"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Identifier != "SAM-1" || issues[1].Identifier != "SAM-2" {
		t.Fatalf("issues = %+v, want SAM-1 and SAM-2", issues)
	}
	if len(afterSeen) != 2 || afterSeen[0] != nil || afterSeen[1] != "cursor-1" {
		t.Fatalf("after cursors = %+v, want [nil cursor-1]", afterSeen)
	}
}

func TestViewerID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !strings.Contains(req.Query, "Viewer") {
			t.Fatalf("query = %s, want Viewer", req.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"user-current"}}}`))
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ViewerID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "user-current" {
		t.Fatalf("ViewerID = %q, want user-current", got)
	}
}

func TestEscalateNeedsHuman(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	queries := []string{}
	varsSeen := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		queries = append(queries, req.Query)
		varsSeen = append(varsSeen, req.Variables)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "IssueTeamByID"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-1","team":{"id":"team-1"}}}}`))
		case strings.Contains(req.Query, "WorkflowStateByName"):
			_, _ = w.Write([]byte(`{"data":{"workflowStates":{"nodes":[{"id":"state-needs-human","name":"Needs Human"}]}}}`))
		case strings.Contains(req.Query, "CommentCreate"):
			_, _ = w.Write([]byte(`{"data":{"commentCreate":{"success":true,"comment":{"id":"comment-1"}}}}`))
		case strings.Contains(req.Query, "IssueUpdateState"):
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true,"issue":{"id":"uuid-1","state":{"name":"Needs Human"}}}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.EscalateNeedsHuman(context.Background(), testIssue(), "successful_continuation_loop", 3)
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 4 {
		t.Fatalf("query count = %d, want 4", len(queries))
	}
	if got := varsSeen[1]["stateName"]; got != "Needs Human" {
		t.Fatalf("stateName = %v, want Needs Human", got)
	}
	body, _ := varsSeen[2]["body"].(string)
	if !strings.Contains(body, "successful_continuation_loop") || !strings.Contains(body, "Successful active turns:** 3") {
		t.Fatalf("comment body missing escalation details: %s", body)
	}
	if got := varsSeen[3]["stateID"]; got != "state-needs-human" {
		t.Fatalf("stateID = %v, want state-needs-human", got)
	}
}

func TestEscalateNeedsHuman_MissingState(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "IssueTeamByID"):
			_, _ = w.Write([]byte(`{"data":{"issue":{"id":"uuid-1","team":{"id":"team-1"}}}}`))
		case strings.Contains(req.Query, "WorkflowStateByName"):
			_, _ = w.Write([]byte(`{"data":{"workflowStates":{"nodes":[]}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.EscalateNeedsHuman(context.Background(), testIssue(), "successful_continuation_loop", 3)
	if !errors.Is(err, ErrLinearMissingState) {
		t.Fatalf("err = %v, want ErrLinearMissingState", err)
	}
}

func TestDynamicToolRunsGraphQLWithHostCredential(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_test" {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Query != "query Issue($id: String!) { issue(id: $id) { identifier } }" {
			t.Fatalf("query = %q", req.Query)
		}
		if req.Variables["id"] != "uuid-1" {
			t.Fatalf("variables = %+v", req.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issue":{"identifier":"SAM-1"}}}`))
	}))
	defer srv.Close()

	c, err := New(Config{Endpoint: srv.URL, APIKey: "lin_api_test", ProjectSlug: "spur"})
	if err != nil {
		t.Fatal(err)
	}
	tool := c.DynamicTool()
	if tool.Name != "linear_graphql" || len(tool.InputSchema) == 0 || tool.Handle == nil {
		t.Fatalf("tool = %+v", tool)
	}
	result, err := tool.Handle(context.Background(), agent.DynamicToolCall{
		Arguments: json.RawMessage(`{
			"query":"query Issue($id: String!) { issue(id: $id) { identifier } }",
			"variables":{"id":"uuid-1"}
		}`),
	})
	if err != nil {
		t.Fatalf("tool handle: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v", result)
	}
	if result.Text != `{"issue":{"identifier":"SAM-1"}}` {
		t.Fatalf("result text = %s", result.Text)
	}
}

func testIssue() domain.Issue {
	return domain.Issue{ID: "uuid-1", Identifier: "SAM-1"}
}
