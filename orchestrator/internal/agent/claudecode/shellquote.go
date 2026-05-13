package claudecode

import (
	"encoding/base64"
	"strings"
)

// shellQuote returns s wrapped in single quotes, escaping any embedded
// single quotes. Suitable for embedding into a bash command line.
//
//	shellQuote("foo")      → 'foo'
//	shellQuote("don't")    → 'don'\''t'
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// base64Encode returns the unpadded base64 of s. Used to ship long
// prompts through bash without quoting concerns.
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
