package providerchecks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/providertransport"
)

const maxResponseBytes = 512 << 10

const (
	maxGitLabPipelines       = 5
	maxGitLabJobsPerPipeline = 20
)

var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

type Snapshot = domain.ProviderCheckObservation

type Client struct {
	Resolver                  credentials.Resolver
	HTTPClient                *http.Client
	AllowPrivateNetworks      bool
	AllowInsecureHTTP         bool
	OwnGitHubAppID            int64
	CollectFailureDiagnostics bool
}

// Fetch observes only the exact immutable head from a previously admitted
// job. Tokens are resolved immediately before the read and never returned.
func (c Client) Fetch(ctx context.Context, job domain.ReviewJob) (Snapshot, error) {
	if c.Resolver == nil || !commitSHA.MatchString(job.HeadSHA) {
		return Snapshot{}, fmt.Errorf("provider checks require a resolver and full commit SHA")
	}
	base, err := c.validBase(job.Provider, job.APIBaseURL)
	if err != nil {
		return Snapshot{}, err
	}
	token, err := c.Resolver.Resolve(ctx, job)
	if err != nil || strings.TrimSpace(token) == "" {
		return Snapshot{}, fmt.Errorf("provider checks credential is unavailable")
	}
	observedAt := time.Now().UTC()
	snapshot := Snapshot{Provider: job.Provider, Repository: job.Repository, HeadSHA: strings.ToLower(job.HeadSHA), State: "observed", ObservedAt: &observedAt, Checks: []domain.ProviderCheck{}}
	switch job.Provider {
	case domain.ProviderGitHub:
		err = c.github(ctx, base, job.Repository, snapshot.HeadSHA, token, &snapshot)
	case domain.ProviderGitLab:
		err = c.gitlab(ctx, base, job.Repository, snapshot.HeadSHA, token, &snapshot)
	default:
		err = fmt.Errorf("provider checks are unsupported for %q", job.Provider)
	}
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (c Client) validBase(provider domain.Provider, raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("provider checks API base is invalid")
	}
	if parsed.Scheme != "https" && !(c.AllowInsecureHTTP && parsed.Scheme == "http") {
		return "", fmt.Errorf("provider checks require HTTPS")
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	switch provider {
	case domain.ProviderGitHub:
		if path != "" && path != "/api/v3" {
			return "", fmt.Errorf("GitHub API base path is invalid")
		}
	case domain.ProviderGitLab:
		if !strings.HasSuffix(path, "/api/v4") {
			return "", fmt.Errorf("GitLab API base path is invalid")
		}
	default:
		return "", fmt.Errorf("provider checks are unsupported for %q", provider)
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func (c Client) github(ctx context.Context, base, repository, sha, token string, snapshot *Snapshot) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[1] == "." || parts[0] == ".." || parts[1] == ".." {
		return fmt.Errorf("GitHub repository is invalid")
	}
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/commits/" + sha
	var runs struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			ID     int64 `json:"id"`
			Output struct {
				Title            string `json:"title"`
				Summary          string `json:"summary"`
				Text             string `json:"text"`
				AnnotationsCount int    `json:"annotations_count"`
			} `json:"output"`
			Name       string `json:"name"`
			HeadSHA    string `json:"head_sha"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
			App        struct {
				ID int64 `json:"id"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if err := c.getJSON(ctx, base+path+"/check-runs?per_page=100", token, domain.ProviderGitHub, &runs); err != nil {
		return err
	}
	if runs.TotalCount > len(runs.CheckRuns) || len(runs.CheckRuns) > 100 {
		snapshot.Truncated = true
	}
	for index, run := range runs.CheckRuns {
		if index >= 100 {
			break
		}
		if !strings.EqualFold(run.HeadSHA, sha) {
			return fmt.Errorf("GitHub check run did not match the reviewed commit")
		}
		state := run.Status
		if run.Status == "completed" {
			state = run.Conclusion
		}
		origin := "unclassified"
		if run.App.ID > 0 && c.OwnGitHubAppID > 0 {
			if run.App.ID == c.OwnGitHubAppID {
				origin = "open_review"
			} else {
				origin = "independent"
			}
		}
		check := domain.ProviderCheck{Kind: "check_run", Name: bounded(run.Name, 180), State: bounded(state, 40), URL: safeHTTPSURL(run.HTMLURL), Origin: origin}
		if c.CollectFailureDiagnostics && origin == "independent" && state == "failure" {
			diagnostic := run.Output.Title + "\n" + run.Output.Summary + "\n" + run.Output.Text
			if run.ID > 0 && run.Output.AnnotationsCount > 0 && run.Output.AnnotationsCount <= 20 {
				var annotations []struct {
					Path       string `json:"path"`
					StartLine  int    `json:"start_line"`
					Message    string `json:"message"`
					RawDetails string `json:"raw_details"`
				}
				endpoint := fmt.Sprintf("%s/repos/%s/%s/check-runs/%d/annotations?per_page=20", base, url.PathEscape(parts[0]), url.PathEscape(parts[1]), run.ID)
				if err := c.getJSON(ctx, endpoint, token, domain.ProviderGitHub, &annotations); err == nil && len(annotations) == run.Output.AnnotationsCount {
					encoded, _ := json.Marshal(annotations)
					diagnostic += "\n" + string(encoded)
				} else {
					diagnostic = ""
				}
			} else if run.Output.AnnotationsCount > 20 {
				diagnostic = ""
			}
			check.Diagnostics, check.FailureClass = classifyDiagnostics(diagnostic, token)
		}
		snapshot.Checks = append(snapshot.Checks, check)
	}
	var statuses struct {
		SHA        string `json:"sha"`
		TotalCount int    `json:"total_count"`
		Statuses   []struct {
			Context   string `json:"context"`
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := c.getJSON(ctx, base+path+"/status?per_page=100", token, domain.ProviderGitHub, &statuses); err != nil {
		return err
	}
	if !strings.EqualFold(statuses.SHA, sha) {
		return fmt.Errorf("GitHub commit status did not match the reviewed commit")
	}
	if statuses.TotalCount > len(statuses.Statuses) || len(statuses.Statuses) > 100 {
		snapshot.Truncated = true
	}
	for index, status := range statuses.Statuses {
		if index >= 100 {
			break
		}
		origin := "independent"
		if strings.EqualFold(strings.TrimSpace(status.Context), "Open Review / Analysis") {
			// Combined statuses omit the GitHub App ID. A same-named context
			// cannot prove independent CI or ownership of our check.
			origin = "unclassified"
		}
		snapshot.Checks = append(snapshot.Checks, domain.ProviderCheck{Kind: "commit_status", Name: bounded(status.Context, 180), State: bounded(status.State, 40), URL: safeHTTPSURL(status.TargetURL), Origin: origin})
	}
	return nil
}

func (c Client) gitlab(ctx context.Context, base, repository, sha, token string, snapshot *Snapshot) error {
	if len(strings.Split(repository, "/")) < 2 || strings.Contains(repository, "..") || strings.Trim(repository, "/") != repository {
		return fmt.Errorf("GitLab repository is invalid")
	}
	var pipelines []struct {
		ID     int64  `json:"id"`
		SHA    string `json:"sha"`
		Status string `json:"status"`
		WebURL string `json:"web_url"`
	}
	projectPath := base + "/projects/" + url.PathEscape(repository)
	endpoint := projectPath + "/pipelines?sha=" + sha + "&per_page=5"
	if err := c.getJSON(ctx, endpoint, token, domain.ProviderGitLab, &pipelines); err != nil {
		return err
	}
	if len(pipelines) >= maxGitLabPipelines {
		snapshot.Truncated = true
	}
	for index, pipeline := range pipelines {
		if index >= maxGitLabPipelines {
			break
		}
		if pipeline.ID < 1 || !strings.EqualFold(pipeline.SHA, sha) {
			return fmt.Errorf("GitLab pipeline did not match the reviewed commit")
		}
		// GitLab external-status pipelines may consist solely of Open Review's
		// own merge-gate status. The pipeline API does not prove independence.
		snapshot.Checks = append(snapshot.Checks, domain.ProviderCheck{Kind: "pipeline", Name: fmt.Sprintf("Pipeline #%d", pipeline.ID), State: bounded(pipeline.Status, 40), URL: safeHTTPSURL(pipeline.WebURL), Origin: "unclassified"})
		var jobs []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			WebURL   string `json:"web_url"`
			Pipeline struct {
				ID int64 `json:"id"`
			} `json:"pipeline"`
		}
		jobsURL := fmt.Sprintf("%s/pipelines/%d/jobs?per_page=%d", projectPath, pipeline.ID, maxGitLabJobsPerPipeline)
		if err := c.getJSON(ctx, jobsURL, token, domain.ProviderGitLab, &jobs); err != nil {
			return fmt.Errorf("read GitLab pipeline jobs: %w", err)
		}
		if len(jobs) >= maxGitLabJobsPerPipeline {
			snapshot.Truncated = true
		}
		for jobIndex, job := range jobs {
			if jobIndex >= maxGitLabJobsPerPipeline {
				break
			}
			if job.ID < 1 || (job.Pipeline.ID > 0 && job.Pipeline.ID != pipeline.ID) {
				return fmt.Errorf("GitLab job did not match the observed pipeline")
			}
			name := bounded(job.Name, 180)
			origin := "unclassified"
			if name != "" && !strings.EqualFold(name, "Open Review / Analysis") {
				origin = "independent"
			}
			check := domain.ProviderCheck{Kind: "job", Name: fmt.Sprintf("Pipeline #%d / %s", pipeline.ID, name), State: bounded(job.Status, 40), URL: safeHTTPSURL(job.WebURL), Origin: origin}
			if c.CollectFailureDiagnostics && origin == "independent" && job.Status == "failed" && jobIndex < 5 {
				diagnostic, err := c.getText(ctx, fmt.Sprintf("%s/jobs/%d/trace", projectPath, job.ID), token, domain.ProviderGitLab)
				if err == nil {
					check.Diagnostics, check.FailureClass = classifyDiagnostics(diagnostic, token)
				}
			}
			snapshot.Checks = append(snapshot.Checks, check)
		}
	}
	return nil
}

func (c Client) getJSON(ctx context.Context, endpoint, token string, provider domain.Provider, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("provider checks request is invalid: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if provider == domain.ProviderGitHub {
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	client := c.HTTPClient
	if client == nil {
		client = providertransport.NewClient(c.AllowPrivateNetworks)
	}
	response, err := httpguard.NoRedirects(client, 20*time.Second).Do(request)
	if err != nil {
		return fmt.Errorf("read provider checks: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("provider checks returned HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read provider checks response: %w", err)
	}
	if len(content) > maxResponseBytes {
		return fmt.Errorf("provider checks response is too large")
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("decode provider checks response: %w", err)
	}
	return nil
}

func safeHTTPSURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func bounded(value string, max int) string {
	value = strings.TrimSpace(value)
	chars := []rune(value)
	if len(chars) > max {
		return string(chars[:max])
	}
	return value
}
