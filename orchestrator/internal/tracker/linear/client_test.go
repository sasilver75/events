package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
