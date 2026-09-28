package domain

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	cliSHA            = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	cliIdempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$`)
	cliRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$`)
)

// CLIReviewInput intentionally identifies an existing provider review. Clone
// destinations and credentials are derived from the trusted installation and
// can never be supplied by a machine caller.
type CLIReviewInput struct {
	InstallationID uuid.UUID  `json:"installation_id"`
	Repository     string     `json:"repository"`
	ReviewNumber   int        `json:"review_number"`
	BaseRef        string     `json:"base_ref"`
	BaseSHA        string     `json:"base_sha"`
	HeadRef        string     `json:"head_ref"`
	HeadSHA        string     `json:"head_sha"`
	Mode           ReviewMode `json:"mode"`
}

type CLIReviewRun struct {
	ReviewRunSummary
	CallerSubject string `json:"caller_subject"`
}

type CLIReviewSubmission struct {
	Run       CLIReviewRun `json:"run"`
	Replayed  bool         `json:"replayed"`
	Coalesced bool         `json:"coalesced"`
}

func NormalizeCLIReviewInput(input CLIReviewInput, idempotencyKey string) (CLIReviewInput, bool) {
	input.Repository = strings.TrimSpace(input.Repository)
	input.BaseRef = strings.TrimSpace(input.BaseRef)
	input.HeadRef = strings.TrimSpace(input.HeadRef)
	input.BaseSHA = strings.ToLower(strings.TrimSpace(input.BaseSHA))
	input.HeadSHA = strings.ToLower(strings.TrimSpace(input.HeadSHA))
	if input.Mode == "" {
		input.Mode = ReviewModeConfigured
	}
	if input.InstallationID == uuid.Nil || input.ReviewNumber < 1 || !input.Mode.Valid() || !cliIdempotencyKey.MatchString(strings.TrimSpace(idempotencyKey)) {
		return CLIReviewInput{}, false
	}
	parts := strings.Split(input.Repository, "/")
	if len(parts) < 2 || len(parts) > 20 {
		return CLIReviewInput{}, false
	}
	for _, part := range parts {
		if !cliRepositoryPart.MatchString(part) || part == "." || part == ".." {
			return CLIReviewInput{}, false
		}
	}
	if !validCLIRef(input.BaseRef) || !validCLIRef(input.HeadRef) || !cliSHA.MatchString(input.BaseSHA) || !cliSHA.MatchString(input.HeadSHA) {
		return CLIReviewInput{}, false
	}
	return input, true
}

func validCLIRef(ref string) bool {
	if ref == "" || len(ref) > 255 || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") {
		return false
	}
	for _, invalid := range []string{" ", "~", "^", ":", "?", "*", "[", "\\"} {
		if strings.Contains(ref, invalid) {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}
