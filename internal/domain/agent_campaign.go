package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"path"
	"regexp"
	"strings"
	"time"
)

// A campaign freezes its request and repository selection before scanning.
// Content is untrusted data; it cannot name commands, credentials or adapters.
type AgentCampaignInput struct {
	IdempotencyKey     string                    `json:"idempotency_key"`
	Title              string                    `json:"title"`
	Mode               string                    `json:"mode"`
	Requirements       string                    `json:"requirements"`
	AcceptanceCriteria []string                  `json:"acceptance_criteria"`
	Paths              []string                  `json:"paths"`
	Search             string                    `json:"search"`
	Regex              bool                      `json:"regex"`
	Replacement        string                    `json:"replacement"`
	ExpectedMatches    *int                      `json:"expected_matches,omitempty"`
	InstallationIDs    []uuid.UUID               `json:"installation_ids"`
	Repositories       []AgentCampaignRepository `json:"repositories"`
	AllRepositories    bool                      `json:"all_repositories"`
	Concurrency        int                       `json:"concurrency"`
}
type AgentCampaignRepository struct {
	InstallationID uuid.UUID `json:"installation_id"`
	Repository     string    `json:"repository"`
}

func (i AgentCampaignInput) Valid() bool {
	if len(i.IdempotencyKey) < 8 || len(i.IdempotencyKey) > 100 || len(strings.TrimSpace(i.Title)) < 3 || len(i.Title) > 200 || (i.Mode != "scan" && i.Mode != "replace" && i.Mode != "docs") || i.Concurrency < 1 || i.Concurrency > 10 || len(i.Paths) == 0 || len(i.Paths) > 30 || len(i.Search) > 1000 || len(i.Replacement) > 4000 || len(i.Requirements) > 12000 || strings.ContainsRune(i.Requirements, 0) || strings.ContainsRune(i.Title, 0) {
		return false
	}
	if i.Mode != "scan" && (len(strings.TrimSpace(i.Requirements)) < 80 || len(i.AcceptanceCriteria) == 0 || len(i.AcceptanceCriteria) > 20) {
		return false
	}
	seenCriteria := map[string]bool{}
	for _, c := range i.AcceptanceCriteria {
		if len(strings.TrimSpace(c)) < 3 || len(c) > 500 || strings.ContainsRune(c, 0) || c != strings.TrimSpace(c) || seenCriteria[c] {
			return false
		}
		seenCriteria[c] = true
	}
	if i.Mode == "replace" && (i.Regex || i.Search == "" || i.Search == i.Replacement) {
		return false
	}
	if i.Mode == "scan" && i.Search == "" {
		return false
	}
	if i.ExpectedMatches != nil && (*i.ExpectedMatches < 1 || *i.ExpectedMatches > 100000) {
		return false
	}
	if i.Regex {
		if _, err := regexp.Compile(i.Search); err != nil {
			return false
		}
	}
	for _, p := range i.Paths {
		if !CampaignPathPatternValid(p) {
			return false
		}
	}
	if i.AllRepositories {
		if len(i.InstallationIDs) == 0 || len(i.InstallationIDs) > 30 || len(i.Repositories) != 0 {
			return false
		}
	} else if len(i.Repositories) == 0 || len(i.Repositories) > 1000 {
		return false
	}
	return true
}
func CampaignPathPatternValid(p string) bool {
	_, err := path.Match(p, "test")
	return err == nil && p != "" && len(p) <= 512 && !strings.HasPrefix(p, "/") && !strings.Contains(p, "..") && !strings.ContainsAny(p, "\\\x00\r\n")
}
func CampaignPathAllowed(patterns []string, file string) bool {
	if file == "" || len(file) > 512 || path.Clean(file) != file || strings.HasPrefix(file, "/") || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\\x00\r\n") {
		return false
	}
	for _, p := range patterns {
		if campaignGlob(strings.Split(p, "/"), strings.Split(file, "/")) {
			return true
		}
	}

	return false
}
func CampaignDigest(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

type AgentCampaign struct {
	ID            uuid.UUID          `json:"id"`
	TenantID      uuid.UUID          `json:"tenant_id,omitempty"`
	Input         AgentCampaignInput `json:"input"`
	RequestSHA256 string             `json:"request_sha256"`
	State         string             `json:"state"`
	Revision      int                `json:"revision"`
	RequestedBy   string             `json:"requested_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}
type AgentCampaignFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Matches int    `json:"matches"`
	Bytes   int    `json:"bytes"`
}
type AgentCampaignScan struct {
	BaseRef       string              `json:"base_ref"`
	BaseSHA       string              `json:"base_sha"`
	Complete      bool                `json:"complete"`
	FilesScanned  int                 `json:"files_scanned"`
	FilesExcluded int                 `json:"files_excluded"`
	BytesScanned  int                 `json:"bytes_scanned"`
	Matches       int                 `json:"matches"`
	Files         []AgentCampaignFile `json:"files"`
	ErrorCode     string              `json:"error_code,omitempty"`
}
type AgentCampaignTarget struct {
	ID                     uuid.UUID         `json:"id"`
	CampaignID             uuid.UUID         `json:"campaign_id"`
	InstallationID         uuid.UUID         `json:"installation_id"`
	Provider               Provider          `json:"provider"`
	APIBaseURL             string            `json:"api_base_url"`
	Repository             string            `json:"repository"`
	State                  string            `json:"state"`
	Scan                   AgentCampaignScan `json:"scan"`
	TaskID                 *uuid.UUID        `json:"task_id,omitempty"`
	Detail                 *AgentTaskDetail  `json:"detail,omitempty"`
	ErrorCode              string            `json:"error_code,omitempty"`
	ErrorMessage           string            `json:"error_message,omitempty"`
	ScanAttempts           int               `json:"scan_attempts"`
	LockedUntil            *time.Time        `json:"locked_until,omitempty"`
	PlanningTask           AgentTask         `json:"-"`
	CredentialRef          string            `json:"-"`
	InstallationExternalID string            `json:"-"`
	WorkerID               string            `json:"-"`
}
type AgentCampaignDetail struct {
	Campaign AgentCampaign         `json:"campaign"`
	Targets  []AgentCampaignTarget `json:"targets"`
	Summary  AgentCampaignSummary  `json:"summary"`
}
type AgentCampaignSummary struct {
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	Closed     bool           `json:"closed"`
	Conclusion string         `json:"conclusion"`
}
type AgentCampaignApproval struct {
	Revision int                         `json:"revision"`
	Plans    []AgentCampaignPlanApproval `json:"plans"`
}
type AgentCampaignPlanApproval struct {
	TaskID   uuid.UUID `json:"task_id"`
	PlanID   uuid.UUID `json:"plan_id"`
	Revision int       `json:"revision"`
	SHA256   string    `json:"sha256"`
}
type AgentCampaignAction struct {
	Revision int    `json:"revision"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
}

// The signed execution envelope binds an authoritative workspace request to
// provider-read paths, before-content digests and an immutable checkout SHA.
type AgentCampaignBinding struct {
	CampaignID    uuid.UUID           `json:"campaign_id"`
	TargetID      uuid.UUID           `json:"target_id"`
	RequestSHA256 string              `json:"request_sha256"`
	Mode          string              `json:"mode"`
	Paths         []string            `json:"paths"`
	Search        string              `json:"search"`
	Replacement   string              `json:"replacement"`
	Files         []AgentCampaignFile `json:"files"`
	Requirements  string              `json:"requirements"`
	Criteria      []string            `json:"criteria"`
}

func (b AgentCampaignBinding) Valid() bool {
	digest, e := hex.DecodeString(b.RequestSHA256)
	if e != nil || len(digest) != sha256.Size {
		return false
	}
	if b.CampaignID == uuid.Nil || b.TargetID == uuid.Nil || len(b.RequestSHA256) != 64 || (b.Mode != "replace" && b.Mode != "docs") || len(b.Paths) == 0 || len(b.Criteria) == 0 {
		return false
	}
	for _, p := range b.Paths {
		if !CampaignPathPatternValid(p) {
			return false
		}
	}
	if b.Mode == "replace" && (b.Search == "" || len(b.Files) == 0 || b.Search == b.Replacement) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range b.Files {
		if !CampaignPathAllowed(b.Paths, f.Path) || len(f.SHA256) != 64 || f.Matches < 0 || seen[f.Path] {
			return false
		}
		seen[f.Path] = true
	}
	return true
}
func (b AgentCampaignBinding) OriginRevision() string {
	return "campaign:" + b.TargetID.String() + ":" + b.RequestSHA256
}
func (d *AgentCampaignDetail) Summarize() {
	s := AgentCampaignSummary{Total: len(d.Targets), Counts: map[string]int{}, Closed: len(d.Targets) > 0}
	for j := range d.Targets {
		t := &d.Targets[j]
		state := t.State
		if t.Detail != nil {
			a := t.Detail.Acceptance
			switch {
			case a != nil && a.State == "accepted":
				state = "accepted"
			case t.Detail.Task.State == "cancelled":
				state = "cancelled"
			case t.Detail.Task.State == "rejected" || t.Detail.Task.State == "needs_attention":
				state = "needs_attention"
			case a != nil:
				state = a.State
			default:
				state = t.Detail.Task.State
			}
		}
		t.State = state
		s.Counts[state]++
		if state != "accepted" && state != "no_match" && state != "scan_complete" {
			s.Closed = false
		}
	}
	if s.Closed {
		s.Conclusion = "All selected repositories have complete scan evidence or accepted requirements."
	} else {
		s.Conclusion = fmt.Sprintf("%d of %d repositories closed; remaining repositories require execution, evidence, or human acceptance.", s.Counts["accepted"]+s.Counts["no_match"]+s.Counts["scan_complete"], s.Total)
	}
	d.Summary = s
}

func campaignGlob(pattern, file []string) bool {
	type pair struct{ p, f int }
	memo := map[pair]bool{}
	visited := map[pair]bool{}
	var match func(int, int) bool
	match = func(p, f int) bool {
		key := pair{p, f}
		if visited[key] {
			return memo[key]
		}
		visited[key] = true
		ok := false
		if p == len(pattern) {
			ok = f == len(file)
		} else if pattern[p] == "**" {
			ok = match(p+1, f) || (f < len(file) && match(p, f+1))
		} else if f < len(file) {
			part, _ := path.Match(pattern[p], file[f])
			ok = part && match(p+1, f+1)
		}
		memo[key] = ok
		return ok
	}
	return match(0, 0)
}
