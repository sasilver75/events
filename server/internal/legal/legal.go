// Package legal serves the Terms of Service over GET /legal/tos.
//
// The ToS markdown is embedded into the server binary at build time. The
// canonical, human-readable copy lives at docs/legal/tos-v<N>.md at the
// repo root (referenced from PRD-v0.md and CLAUDE.md). The copy under
// server/internal/legal/tos/ is the build artifact go:embed reads — Docker's
// build context is server/, so docs/ is not visible at build time.
//
// Keep the two copies byte-identical. The `make legal-sync` target copies
// docs/legal/*.md → server/internal/legal/tos/ and `make legal-check`
// diffs them so a stale embed never ships.
package legal

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed tos/tos-v1.md
var tosV1 string

// Version is the wire string the iOS client persists in users.tos_version
// and echoes back on POST /users/me/profile. Bump in lockstep with the
// filename when the ToS revises.
const Version = "v1"

type response struct {
	Version string `json:"version"`
	Content string `json:"content"`
}

// Get serves the current ToS. Public — no JWT required, since iOS shows
// it during signup before the user has a session.
func Get(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{Version: Version, Content: tosV1})
}
