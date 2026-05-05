package lifecycle

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nklisch/agentbox/internal/exitcode"
	"github.com/nklisch/agentbox/internal/project"
)

var hexID = regexp.MustCompile(`^[a-f0-9]{12}$`)

// ResolveID resolves a user-provided string to a 12-char ProjectID:
//   - "."               → project_id of current $PWD
//   - "agentbox-<id>"   → strips the prefix
//   - 12-char hex       → returned as-is
//   - shorter prefix    → unique prefix-match against existing boxes
//
// Errors:
//   - exit 4 (NotFound):    no match (when prefix-matching against boxes)
//   - exit 2 (InvalidArgs): empty input or ambiguous prefix
func (l *Lifecycle) ResolveID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", exitcode.New(exitcode.InvalidArgs, "project_id required")
	}

	// Dot → current $PWD's project_id (no Runtime call needed).
	if input == "." {
		id, _, err := project.Resolve()
		if err != nil {
			return "", exitcode.Wrap(exitcode.Generic, err)
		}
		return id, nil
	}

	// Strip "agentbox-" prefix if present.
	input = strings.TrimPrefix(input, "agentbox-")

	// Exact 12-char hex → no lookup needed.
	if hexID.MatchString(input) {
		return input, nil
	}

	// Prefix match against existing boxes (running OR stopped).
	boxes, err := l.Runtime.Ls(true)
	if err != nil {
		return "", exitcode.Wrap(exitcode.Generic, err)
	}
	var matches []string
	for _, b := range boxes {
		if strings.HasPrefix(b.ProjectID, input) {
			matches = append(matches, b.ProjectID)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", exitcode.New(exitcode.NotFound,
			"no box matches %q", input)
	case 1:
		return matches[0], nil
	default:
		return "", exitcode.New(exitcode.InvalidArgs,
			"ambiguous prefix %q matches: %s", input, strings.Join(matches, ", "))
	}
}
