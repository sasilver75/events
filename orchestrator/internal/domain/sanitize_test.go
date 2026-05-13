package domain

import "testing"

func TestWorkspaceKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"SAM-12", "SAM-12"},
		{"SAM-12.3", "SAM-12.3"},
		{"a/b 12", "a_b_12"},
		{"ABC-123", "ABC-123"},
		{"foo:bar", "foo_bar"},
		{"foo bar/baz", "foo_bar_baz"},
		{"_already_clean.", "_already_clean."},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := WorkspaceKey(tc.in)
			if got != tc.want {
				t.Errorf("WorkspaceKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
