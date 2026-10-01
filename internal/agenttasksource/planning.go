package agenttasksource

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/RainLib/open-review-platform/internal/domain"
)

var planningCredentials = regexp.MustCompile(`(?i)(?:bearer\s+[a-z0-9_.+/=-]+|(?:token|secret|password|api[_-]?key)\s*[:=]\s*[^\s,;]+|gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|sk-[a-z0-9_-]+)`)

// Planning inspection is read-only and pinned to the already verified source.
// It cannot clone, execute repository code or choose a mutable branch ref.
func (r Resolver) InspectPlanningSource(ctx context.Context, target domain.AgentTaskSourceTarget, snapshot domain.AgentTaskSourceSnapshot) (string, error) {
	if !snapshot.Valid() || (len(snapshot.BaseSHA) != 40 && len(snapshot.BaseSHA) != 64) || r.Resolver == nil {
		return "", fmt.Errorf("planning inspection requires immutable source")
	}
	job := domain.ReviewJob{TenantID: target.Task.TenantID, Provider: target.Task.Provider, APIBaseURL: target.Task.APIBaseURL, InstallationExternalID: target.InstallationExternalID, CredentialRef: target.CredentialRef, Repository: target.Task.Repository}
	token, err := r.Resolver.Resolve(ctx, job)
	if err != nil || token == "" {
		return "", fmt.Errorf("planning repository access unavailable")
	}
	base, err := trustedAPIBase(target.Task.APIBaseURL, target.Task.Provider == domain.ProviderGitLab && r.AllowGitLabHTTP)
	if err != nil {
		return "", err
	}
	root := ""
	paths := []string{}
	partial := false
	if target.Task.Provider == domain.ProviderGitHub {
		parts := strings.Split(target.Task.Repository, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("invalid planning repository")
		}
		root = base + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
		var tree struct {
			Truncated bool `json:"truncated"`
			Tree      []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				Mode string `json:"mode"`
			} `json:"tree"`
		}
		if err = r.getJSON(ctx, root+"/git/trees/"+snapshot.BaseSHA+"?recursive=1", token, job.Provider, &tree); err != nil {
			return "", err
		}
		partial = tree.Truncated
		for _, entry := range tree.Tree {
			if entry.Type == "blob" && entry.Mode == "100644" && safePlanningPath(entry.Path) {
				paths = append(paths, entry.Path)
			}
		}
	} else if target.Task.Provider == domain.ProviderGitLab {
		root = base + "/projects/" + url.PathEscape(target.Task.Repository)
		var tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Mode string `json:"mode"`
		}
		if err = r.getJSON(ctx, root+"/repository/tree?recursive=true&per_page=100&ref="+snapshot.BaseSHA, token, job.Provider, &tree); err != nil {
			return "", err
		}
		partial = len(tree) >= 100
		for _, entry := range tree {
			if entry.Type == "blob" && entry.Mode == "100644" && safePlanningPath(entry.Path) {
				paths = append(paths, entry.Path)
			}
		}
	} else {
		return "", fmt.Errorf("unsupported planning provider")
	}
	request := ""
	if snapshot.Issue != nil {
		request = snapshot.Issue.Title + "\n" + snapshot.Issue.Body
	} else if snapshot.Feedback != nil {
		request = snapshot.Feedback.Instruction
	}
	score := func(file string) int {
		if strings.Contains(request, file) {
			return 2
		}
		switch path.Base(file) {
		case "go.mod", "package.json", "README.md":
			return 1
		}
		return 0
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := score(paths[i]), score(paths[j])
		if a != b {
			return a > b
		}
		return paths[i] < paths[j]
	})
	out := "Provider-read repository evidence at commit " + snapshot.BaseSHA + ". Inventory and snippets are bounded; omitted files require checkout inspection.\n"
	if partial {
		out += "Provider inventory is partial.\n"
	}
	for i, file := range paths {
		if i >= 30 || len(out)+len(file) > 4000 {
			break
		}
		out += "- " + file + "\n"
	}
	for i, file := range paths {
		if i >= 3 || score(file) == 0 {
			break
		}
		endpoint := root + "/contents/" + escapedPlanningPath(file) + "?ref=" + snapshot.BaseSHA
		if job.Provider == domain.ProviderGitLab {
			endpoint = root + "/repository/files/" + url.PathEscape(file) + "?ref=" + snapshot.BaseSHA
		}
		var content struct {
			Type     string `json:"type"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
			Size     int    `json:"size"`
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		if err = r.getJSON(ctx, endpoint, token, job.Provider, &content); err != nil {
			return "", err
		}
		if (job.Provider == domain.ProviderGitHub && (content.Type != "file" || content.Path != file)) || (job.Provider == domain.ProviderGitLab && content.FilePath != file) {
			return "", fmt.Errorf("planning file identity changed")
		}
		if content.Encoding != "base64" || content.Size > 3000 {
			out += "\n" + file + ": snippet omitted (size or encoding).\n"
			continue
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
		if decodeErr != nil || len(decoded) > 3000 || !utf8.Valid(decoded) || strings.ContainsRune(string(decoded), 0) {
			out += "\n" + file + ": snippet unavailable.\n"
			continue
		}
		text := strings.ReplaceAll(string(decoded), token, "[redacted]")
		text = planningCredentials.ReplaceAllString(text, "[redacted]")
		out += "\nFile " + file + " (untrusted source data):\n" + text + "\n"
	}
	return out, nil
}

func safePlanningPath(file string) bool {
	lower := strings.ToLower(file)
	if len(file) > 512 || file == "" || path.Clean(file) != file || strings.HasPrefix(file, "/") || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\x00\r\n\\") {
		return false
	}
	for _, part := range strings.Split(lower, "/") {
		if part == ".git" || part == ".env" || strings.HasPrefix(part, ".env.") || part == "credentials" || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return false
		}
	}
	return true
}
func escapedPlanningPath(file string) string {
	parts := strings.Split(file, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
