package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader error categories. Symphony spec §5.5.
var (
	ErrMissingWorkflowFile = errors.New("missing_workflow_file")
	ErrWorkflowParse       = errors.New("workflow_parse_error")
	ErrFrontMatterNotMap   = errors.New("workflow_front_matter_not_a_map")
)

// Load reads and parses a WORKFLOW.md file from disk. Symphony spec §5.2.
//
// Parsing rules per the spec:
//   - If file starts with "---", parse lines until the next "---" as YAML
//     front matter.
//   - Remaining lines become the prompt body.
//   - If front matter is absent, the entire file is the prompt body and
//     Config is an empty map.
//   - YAML front matter must decode to a map/object; non-map YAML is an
//     error.
//   - Prompt body is trimmed.
func Load(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrMissingWorkflowFile, path)
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrMissingWorkflowFile, path, err)
	}
	return Parse(data)
}

// Parse is the in-memory equivalent of Load. Symphony spec §5.2.
func Parse(data []byte) (*Definition, error) {
	frontMatter, body, hadFrontMatter := splitFrontMatter(data)

	def := &Definition{
		Config:         map[string]any{},
		PromptTemplate: strings.TrimSpace(string(body)),
	}

	if !hadFrontMatter {
		return def, nil
	}

	var root any
	if err := yaml.Unmarshal(frontMatter, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}
	// An empty front matter section (just "---\n---") yields nil; treat as empty map.
	if root == nil {
		return def, nil
	}
	m, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: top-level YAML must be a map, got %T", ErrFrontMatterNotMap, root)
	}
	def.Config = m
	return def, nil
}

// splitFrontMatter separates the YAML front matter from the Markdown body.
// Returns (frontMatter, body, hadFrontMatter).
//
// Front matter is the region between the first "---\n" (which must be the
// very first line) and the next "---\n" or "---" at EOF. Anything else
// counts as "no front matter" and the whole file is body.
func splitFrontMatter(data []byte) ([]byte, []byte, bool) {
	const sep = "---"
	// Front matter must start the file. Tolerate UTF-8 BOM and a leading
	// newline-free presence of "---" on the first line.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	// Lines must be \n-terminated for the simple split below.
	lines := bytes.SplitN(data, []byte("\n"), 2)
	if len(lines) < 2 || strings.TrimRight(string(lines[0]), "\r") != sep {
		return nil, data, false
	}
	rest := lines[1]
	// Find the closing "---" on its own line.
	closingIdx := findClosingSeparator(rest)
	if closingIdx < 0 {
		// No closing fence — treat as no front matter to avoid silently
		// consuming the whole file.
		return nil, data, false
	}
	front := rest[:closingIdx]
	body := rest[closingIdx:]
	// Strip the closing fence line (and following newline if any) from body.
	body = stripLeadingClosingFence(body)
	return front, body, true
}

// findClosingSeparator returns the byte offset of the start of the line
// containing the closing "---" fence, or -1 if not found.
func findClosingSeparator(rest []byte) int {
	offset := 0
	for offset < len(rest) {
		nl := bytes.IndexByte(rest[offset:], '\n')
		var line []byte
		if nl < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+nl]
		}
		if strings.TrimRight(string(line), "\r") == "---" {
			return offset
		}
		if nl < 0 {
			return -1
		}
		offset += nl + 1
	}
	return -1
}

func stripLeadingClosingFence(body []byte) []byte {
	if !bytes.HasPrefix(body, []byte("---")) {
		return body
	}
	body = body[3:]
	// Trim a single CR (Windows line endings) then a single LF.
	body = bytes.TrimPrefix(body, []byte("\r"))
	body = bytes.TrimPrefix(body, []byte("\n"))
	return body
}
