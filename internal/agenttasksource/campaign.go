package agenttasksource

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/RainLib/open-review-platform/internal/domain"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ScanCampaign reads every eligible file in the explicit path scope at one
// commit. A provider truncation or budget limit yields incomplete evidence,
// never a no-match result. No source text or credentials enter the report.
func (r Resolver) ScanCampaign(ctx context.Context, t domain.AgentCampaignTarget, i domain.AgentCampaignInput) (domain.AgentCampaignScan, error) {
	out := domain.AgentCampaignScan{Files: []domain.AgentCampaignFile{}}
	if r.Resolver == nil {
		return out, fmt.Errorf("campaign credential resolver unavailable")
	}
	job := domain.ReviewJob{Provider: t.Provider, APIBaseURL: t.APIBaseURL, TenantID: t.PlanningTask.TenantID, InstallationID: t.InstallationID, InstallationExternalID: t.InstallationExternalID, CredentialRef: t.CredentialRef, Repository: t.Repository}
	token, err := r.Resolver.Resolve(ctx, job)
	if err != nil || token == "" {
		return out, fmt.Errorf("campaign repository access unavailable")
	}
	task := domain.AgentTask{Provider: t.Provider, APIBaseURL: t.APIBaseURL, Repository: t.Repository}
	var base domain.AgentTaskSourceSnapshot
	if t.Scan.BaseSHA != "" {
		base = domain.AgentTaskSourceSnapshot{BaseRef: t.Scan.BaseRef, BaseSHA: t.Scan.BaseSHA}
	} else if t.Provider == domain.ProviderGitHub {
		base, err = r.github(ctx, task, token)
	} else if t.Provider == domain.ProviderGitLab {
		base, err = r.gitLab(ctx, task, token)
	} else {
		err = fmt.Errorf("unsupported provider")
	}
	if err != nil {
		return out, err
	}
	if !base.Valid() || (len(base.BaseSHA) != 40 && len(base.BaseSHA) != 64) {
		return out, fmt.Errorf("invalid immutable campaign source")
	}
	out.BaseRef, out.BaseSHA = base.BaseRef, base.BaseSHA
	api, err := trustedAPIBase(t.APIBaseURL, t.Provider == domain.ProviderGitLab && r.AllowGitLabHTTP)
	if err != nil {
		return out, err
	}
	root := api + "/projects/" + url.PathEscape(t.Repository)
	type entry struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Mode string `json:"mode"`
	}
	entries := []entry{}
	if t.Provider == domain.ProviderGitHub {
		parts := strings.Split(t.Repository, "/")
		if len(parts) != 2 {
			return out, fmt.Errorf("invalid repository")
		}
		root = api + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
		var tree struct {
			Truncated bool    `json:"truncated"`
			Tree      []entry `json:"tree"`
		}
		if err = r.getJSON(ctx, root+"/git/trees/"+base.BaseSHA+"?recursive=1", token, t.Provider, &tree); err != nil {
			return out, err
		}
		if tree.Truncated {
			out.ErrorCode = "tree_truncated"
			return out, nil
		}
		entries = tree.Tree
	} else {
		exhausted := false
		for page := 1; page <= 100; page++ {
			var tree []entry
			if err = r.getJSON(ctx, fmt.Sprintf("%s/repository/tree?recursive=true&per_page=100&page=%d&ref=%s", root, page, base.BaseSHA), token, t.Provider, &tree); err != nil {
				return out, err
			}
			entries = append(entries, tree...)
			if len(tree) < 100 {
				exhausted = true
				break
			}
		}
		if !exhausted {
			out.ErrorCode = "tree_page_limit"
			return out, nil
		}
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Path < entries[b].Path })
	seen := map[string]bool{}
	var expression *regexp.Regexp
	if i.Regex {
		expression, err = regexp.Compile(i.Search)
		if err != nil {
			return out, err
		}
	}
	for _, e := range entries {
		if !domain.CampaignPathAllowed(i.Paths, e.Path) || e.Type == "tree" {
			continue
		}
		if seen[e.Path] {
			out.ErrorCode = "duplicate_tree_path"
			return out, nil
		}
		seen[e.Path] = true
		if e.Type != "blob" || (e.Mode != "100644" && e.Mode != "100755") || !safePlanningPath(e.Path) {
			out.FilesExcluded++
			continue
		}
		if out.FilesScanned >= 1000 || out.BytesScanned >= 20<<20 {
			out.ErrorCode = "scan_budget_exceeded"
			return out, nil
		}
		endpoint := root + "/contents/" + escapedPlanningPath(e.Path) + "?ref=" + base.BaseSHA
		if t.Provider == domain.ProviderGitLab {
			endpoint = root + "/repository/files/" + url.PathEscape(e.Path) + "?ref=" + base.BaseSHA
		}
		var f struct {
			Type     string `json:"type"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
			Size     int    `json:"size"`
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		if err = r.getJSON(ctx, endpoint, token, t.Provider, &f); err != nil {
			return out, err
		}
		if (t.Provider == domain.ProviderGitHub && (f.Type != "file" || f.Path != e.Path)) || (t.Provider == domain.ProviderGitLab && f.FilePath != e.Path) {
			out.ErrorCode = "file_identity_changed"
			return out, nil
		}
		if f.Encoding != "base64" || f.Size > 500000 {
			out.ErrorCode = "file_scan_unavailable"
			return out, nil
		}
		raw, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
		if decodeErr != nil || len(raw) != f.Size {
			out.ErrorCode = "file_content_invalid"
			return out, nil
		}
		if !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
			out.FilesExcluded++
			continue
		}
		count := strings.Count(string(raw), i.Search)
		if i.Search == "" {
			count = 0
		}
		if expression != nil {
			count = len(expression.FindAllIndex(raw, -1))
		}
		out.FilesScanned++
		out.BytesScanned += len(raw)
		out.Matches += count
		if out.Matches > 100000 {
			out.ErrorCode = "match_budget_exceeded"
			return out, nil
		}
		if count > 0 || i.Mode == "docs" {
			out.Files = append(out.Files, domain.AgentCampaignFile{Path: e.Path, SHA256: contentDigest(raw), Matches: count, Bytes: len(raw)})
		}
	}
	out.Complete = true
	return out, nil
}
func contentDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

func (r Resolver) VerifyCampaignBase(ctx context.Context, task domain.AgentTask, token string) error {
	if !task.Provider.Valid() || strings.TrimSpace(token) == "" || !(domain.AgentTaskSourceSnapshot{BaseRef: task.SourceBaseRef, BaseSHA: task.SourceBaseSHA}).Valid() {
		return fmt.Errorf("invalid frozen campaign source")
	}
	var actual domain.AgentTaskSourceSnapshot
	var err error
	// The frozen branch is checked explicitly, rather than trusting a changed
	// default branch. Publication never silently rebases an approved campaign.
	api, err := trustedAPIBase(task.APIBaseURL, task.Provider == domain.ProviderGitLab && r.AllowGitLabHTTP)
	if err != nil {
		return err
	}
	if task.Provider == domain.ProviderGitHub {
		parts := strings.Split(task.Repository, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid campaign repository")
		}
		var c struct {
			SHA string `json:"sha"`
		}
		err = r.getJSON(ctx, api+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])+"/commits/"+url.PathEscape(task.SourceBaseRef), token, task.Provider, &c)
		actual = domain.AgentTaskSourceSnapshot{BaseRef: task.SourceBaseRef, BaseSHA: c.SHA}
	} else {
		var c struct {
			Commit struct {
				ID string `json:"id"`
			} `json:"commit"`
		}
		err = r.getJSON(ctx, api+"/projects/"+url.PathEscape(task.Repository)+"/repository/branches/"+url.PathEscape(task.SourceBaseRef), token, task.Provider, &c)
		actual = domain.AgentTaskSourceSnapshot{BaseRef: task.SourceBaseRef, BaseSHA: c.Commit.ID}
	}
	if err != nil {
		return err
	}
	if !actual.Valid() || actual.BaseSHA != task.SourceBaseSHA {
		return fmt.Errorf("campaign base changed; a new scan and plan approval are required")
	}
	return nil
}
