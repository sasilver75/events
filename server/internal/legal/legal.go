// Package legal serves immutable legal-document content (ToS today; Privacy
// Policy when it lands). The current ToS markdown lives in docs/legal/ and is
// loaded once at startup; the file's basename is the version string clients
// store on their public.users row to detect re-acceptance prompts.
package legal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Handler struct {
	tosVersion string
	tosBody    string
}

// New loads the ToS markdown from disk once. The version is derived from the
// filename: tos-v1.md → "v1". Returns an error if the file is missing,
// unreadable, or the basename doesn't match the expected pattern.
func New(tosPath string) (*Handler, error) {
	body, err := os.ReadFile(tosPath)
	if err != nil {
		return nil, fmt.Errorf("read ToS at %s: %w", tosPath, err)
	}
	base := filepath.Base(tosPath)
	const prefix, suffix = "tos-", ".md"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, suffix) {
		return nil, fmt.Errorf("ToS filename %q does not match tos-<version>.md", base)
	}
	version := strings.TrimSuffix(strings.TrimPrefix(base, prefix), suffix)
	if version == "" {
		return nil, fmt.Errorf("ToS filename %q has empty version", base)
	}
	return &Handler{tosVersion: version, tosBody: string(body)}, nil
}

// TOSVersion is the canonical version string for the loaded ToS document.
// POST /users/me/profile validates the client's submitted version against
// this so a stale signup form can't accept an out-of-date ToS.
func (h *Handler) TOSVersion() string {
	return h.tosVersion
}

type tosResponse struct {
	Version string `json:"version"`
	Content string `json:"content"`
}

// GetTOS handles GET /legal/tos. Public — no JWT required.
func (h *Handler) GetTOS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(tosResponse{
		Version: h.tosVersion,
		Content: h.tosBody,
	})
}
