package ocr

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RainLib/open-review-platform/internal/domain"
)

const (
	maxFindingSourceFileBytes = 256 * 1024
	maxFindingExcerptBytes    = 8 * 1024
	maxFindingPatchBytes      = 16 * 1024
	maxFindingReplacement     = 4 * 1024
	maxFindingRangeLines      = 20
	maxFindingEvidenceLookups = 100
)

var patchableFindingPath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// captureFindingSourceEvidence runs inside the credential-owning review worker
// against the exact Git range OCR just analyzed. Missing, binary, oversized,
// unmodified or ambiguous model locations remain without source evidence.
// An evidence lookup failure never turns a completed analysis into a retry.
func (e Executor) captureFindingSourceEvidence(ctx context.Context, directory, base, head string, findings []domain.Finding) {
	if len(findings) == 0 {
		return
	}
	changed, err := e.gitOutput(ctx, directory, "diff", "--no-renames", "--name-only", "-z", "--diff-filter=ACMRT", base, head, "--")
	if err != nil {
		return
	}
	changedPaths := make(map[string]bool)
	for _, raw := range bytes.Split(changed, []byte{0}) {
		if len(raw) > 0 {
			changedPaths[string(raw)] = true
		}
	}
	type fileEvidence struct {
		lines     []string
		patchable bool
	}
	cache := make(map[string]fileEvidence)
	for index := range findings[:min(len(findings), maxFindingEvidenceLookups)] {
		finding := &findings[index]
		if !changedPaths[finding.Path] || finding.StartLine < 1 || finding.EndLine < finding.StartLine || finding.EndLine-finding.StartLine+1 > maxFindingRangeLines {
			continue
		}
		file, cached := cache[finding.Path]
		if !cached {
			file.lines, file.patchable = e.exactHeadTextLines(ctx, directory, head, finding.Path)
			cache[finding.Path] = file
		}
		lines := file.lines
		if len(lines) == 0 || finding.EndLine > len(lines) {
			continue
		}
		first := max(1, finding.StartLine-3)
		last := min(len(lines), finding.EndLine+3)
		excerpt := strings.Join(lines[first-1:last], "\n")
		if len(excerpt) <= maxFindingExcerptBytes {
			finding.CodeExcerpt = excerpt
			finding.CodeExcerptStartLine = first
		}
		if !file.patchable {
			continue
		}
		patch := proposedFindingPatch(finding.Path, lines, finding.StartLine, finding.EndLine, finding.SuggestionCode)
		if patch == "" || len(patch) > maxFindingPatchBytes {
			continue
		}
		// The proposed hunk must apply to this checkout's exact head. This is
		// structural patch validation, never a claim that it builds or is safe.
		if err := e.gitRun(ctx, directory, []byte(patch), "apply", "--check", "-"); err == nil {
			finding.ProposedPatch = patch
		}
	}
}

func (e Executor) exactHeadTextLines(ctx context.Context, directory, head, path string) ([]string, bool) {
	listing, err := e.gitOutput(ctx, directory, "ls-tree", "-z", head, "--", path)
	if err != nil {
		return nil, false
	}
	var objectID string
	for _, entry := range bytes.Split(listing, []byte{0}) {
		fields := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(fields) != 2 || string(fields[1]) != path {
			continue
		}
		metadata := strings.Fields(string(fields[0]))
		if len(metadata) != 3 || metadata[1] != "blob" || (metadata[0] != "100644" && metadata[0] != "100755") {
			return nil, false // Never follow a symlink or inspect a submodule.
		}
		objectID = metadata[2]
		break
	}
	if objectID == "" {
		return nil, false
	}
	sizeOutput, err := e.gitOutput(ctx, directory, "cat-file", "-s", objectID)
	if err != nil {
		return nil, false
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(sizeOutput)))
	if err != nil || size < 1 || size > maxFindingSourceFileBytes {
		return nil, false
	}
	content, err := e.gitOutput(ctx, directory, "cat-file", "blob", objectID)
	if err != nil || len(content) != size || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, false
	}
	// A proposed hunk is only generated for LF-terminated text. Preserve the
	// exact bytes for context; CRLF and no-final-newline files are inspectable
	// but intentionally receive no automatic patch.
	lines := strings.Split(string(content), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, bytes.HasSuffix(content, []byte{'\n'}) && bytes.IndexByte(content, '\r') < 0
}

func proposedFindingPatch(path string, lines []string, first, last int, replacement string) string {
	if !patchableFindingPath.MatchString(path) || cleanPath(path) != path ||
		len(replacement) == 0 || len(replacement) > maxFindingReplacement ||
		strings.ContainsAny(replacement, "\x00\r") || strings.Contains(replacement, "```") ||
		first < 1 || last < first || last > len(lines) ||
		len(lines) == 0 || strings.Contains(lines[len(lines)-1], "\r") {
		return ""
	}
	for _, line := range lines {
		if strings.Contains(line, "\r") {
			return ""
		}
	}
	replacement = strings.TrimSuffix(replacement, "\n")
	newLines := strings.Split(replacement, "\n")
	if len(newLines) == 0 || len(newLines) > 80 || (len(newLines) == 1 && newLines[0] == "") {
		return ""
	}
	if strings.Join(lines[first-1:last], "\n") == replacement {
		return ""
	}
	before := max(0, first-4)
	after := min(len(lines), last+3)
	var patch strings.Builder
	fmt.Fprintf(&patch, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n",
		path, path, path, path, before+1, after-before, before+1, after-before-(last-first+1)+len(newLines))
	for _, line := range lines[before : first-1] {
		fmt.Fprintf(&patch, " %s\n", line)
	}
	for _, line := range lines[first-1 : last] {
		fmt.Fprintf(&patch, "-%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&patch, "+%s\n", line)
	}
	for _, line := range lines[last:after] {
		fmt.Fprintf(&patch, " %s\n", line)
	}
	return patch.String()
}
