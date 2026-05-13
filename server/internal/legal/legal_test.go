package legal_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sasilver75/events/server/internal/legal"
)

func TestGetTOS(t *testing.T) {
	dir := t.TempDir()
	body := "# Spur Terms of Service — v7\nbody text"
	path := filepath.Join(dir, "tos-v7.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	h, err := legal.New(path)
	if err != nil {
		t.Fatalf("legal.New: %v", err)
	}
	if got, want := h.TOSVersion(), "v7"; got != want {
		t.Errorf("version: got %q, want %q", got, want)
	}

	rec := httptest.NewRecorder()
	h.GetTOS(rec, httptest.NewRequest(http.MethodGet, "/legal/tos", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var got struct {
		Version string `json:"version"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != "v7" {
		t.Errorf("response version: got %q, want v7", got.Version)
	}
	if got.Content != body {
		t.Errorf("response content mismatch")
	}
}

func TestNewRejectsBadFilename(t *testing.T) {
	dir := t.TempDir()
	cases := []string{"terms-v1.md", "tos.md", "tos-v1.txt", "tos-.md"}
	for _, name := range cases {
		path := filepath.Join(dir, name)
		_ = os.WriteFile(path, []byte("x"), 0o600)
		if _, err := legal.New(path); err == nil {
			t.Errorf("New(%q): expected error, got nil", name)
		}
	}
}

func TestNewMissingFile(t *testing.T) {
	if _, err := legal.New(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Errorf("expected error for missing file, got nil")
	}
}
