package domain

// Workspace is the per-issue execution environment. Symphony spec §4.1.4.
//
// In the spec, a Workspace corresponds to a filesystem directory. In Spur's
// adaptation, a Workspace corresponds to a Tart VM clone — the Path field
// holds the Tart VM name (e.g. "spur-ticket-SAM-12"), not a filesystem
// path. The Workspace Manager (workspace/tart) knows how to translate
// between the two.
//
// The safety invariants from spec §9.5 still hold:
//   - The agent runs inside the workspace (i.e. inside the VM whose
//     name matches workspace.Path), never outside it.
//   - The workspace key is sanitized (see WorkspaceKey).
type Workspace struct {
	// Path is the workspace identifier. In Spur this is the Tart VM
	// name. Naming convention: "spur-ticket-<workspace-key>".
	Path string

	// WorkspaceKey is the sanitized issue identifier (spec §4.2).
	// Derived from issue.identifier by replacing any character not
	// in [A-Za-z0-9._-] with _.
	WorkspaceKey string

	// CreatedNow is true if this workspace was created during the
	// current call (vs. reused from a prior run). Gates the
	// `after_create` hook per spec §5.3.4.
	CreatedNow bool
}
