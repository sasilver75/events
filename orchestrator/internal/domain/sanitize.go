package domain

import "regexp"

// workspaceKeyForbidden matches any character disallowed in a workspace key.
// Symphony spec §4.2: "Replace any character not in [A-Za-z0-9._-] with _."
var workspaceKeyForbidden = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// WorkspaceKey returns the sanitized workspace key derived from an issue
// identifier. Symphony spec §4.2 / §9.5 Invariant 3.
//
//	WorkspaceKey("SAM-12")  → "SAM-12"  (already valid)
//	WorkspaceKey("a/b 12")  → "a_b_12"
//	WorkspaceKey("")         → ""        (caller must reject empty input)
func WorkspaceKey(issueIdentifier string) string {
	return workspaceKeyForbidden.ReplaceAllString(issueIdentifier, "_")
}
