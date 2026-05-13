package tart

import "os"

// processEnviron exists as a tiny wrapper so tests can override host env
// without touching the global os.Environ() in test runs.
func processEnviron() []string {
	return os.Environ()
}
