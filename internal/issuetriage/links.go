package issuetriage

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// FileLinkPolicy contains deployment-owned browser origins. An Issue or model
// result can name a repository path, but cannot choose the link's host.
type FileLinkPolicy struct {
	GitHubPublicBaseURL       string
	GitHubPublicForAPIBaseURL string
	GitLabPublicBaseURL       string
	GitLabPublicForAPIBaseURL string
	ConsoleBaseURL            string
	AllowGitLabHTTP           bool
}

// ConsoleAnalysis links to this exact retained Issue analysis. Both the host
// and workspace slug come from trusted deployment/database state, never Issue
// text or a model response. An absent or unsafe public origin omits the link
// without preventing Issue triage.
func (policy FileLinkPolicy) ConsoleAnalysis(job domain.ProviderIssueAnalysisJob) string {
	if job.ID == uuid.Nil || job.TenantSlug == "" ||
		!issueLinkSegments(job.TenantSlug, false) || strings.Contains(job.TenantSlug, "/") {
		return ""
	}
	base, err := issueLinkBase(policy.ConsoleBaseURL, false)
	if err != nil || base.Scheme != "https" || (base.Path != "" && base.Path != "/") || base.RawPath != "" {
		return ""
	}
	base.Path = "/" + job.TenantSlug + "/provider-issues/" + job.ID.String()
	base.RawPath = "/" + url.PathEscape(job.TenantSlug) + "/provider-issues/" + job.ID.String()
	return base.String()
}

func (policy FileLinkPolicy) Validate() error {
	for _, pair := range []struct {
		provider domain.Provider
		public   string
		api      string
	}{
		{domain.ProviderGitHub, policy.GitHubPublicBaseURL, policy.GitHubPublicForAPIBaseURL},
		{domain.ProviderGitLab, policy.GitLabPublicBaseURL, policy.GitLabPublicForAPIBaseURL},
	} {
		if (pair.public == "") != (pair.api == "") {
			return fmt.Errorf("Issue file link public origin requires its exact API pairing")
		}
		if pair.public == "" {
			continue
		}
		public, err := issueLinkBase(pair.public, pair.provider == domain.ProviderGitLab && policy.AllowGitLabHTTP)
		if err != nil || (public.Scheme == "http" && !issueLinkLoopback(public.Hostname())) {
			return fmt.Errorf("Issue file link public origin is invalid")
		}
		api, err := issueLinkBase(pair.api, pair.provider == domain.ProviderGitLab && policy.AllowGitLabHTTP)
		if err != nil || (pair.provider == domain.ProviderGitLab && !strings.HasSuffix(api.Path, "/api/v4")) {
			return fmt.Errorf("Issue file link API pairing is invalid")
		}
	}
	return nil
}

func issueLinkLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func issueLinkBase(raw string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(raw), "/"))
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) || strings.ContainsAny(parsed.Path, "\\\x00\r\n\t") {
		return nil, fmt.Errorf("Issue file link origin is invalid")
	}
	return parsed, nil
}

func issueLinkSegments(value string, allowSpaces bool) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\x00\r\n\t") || (!allowSpaces && strings.ContainsAny(segment, " ?#")) {
			return false
		}
	}
	return true
}

func issueLinkEscape(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// File returns a navigation link to the provider's current default branch.
// It deliberately does not assert that the Issue analysis inspected that file
// or that HEAD still points to the same content when a reader opens the link.
func (policy FileLinkPolicy) File(job domain.ProviderIssueAnalysisJob, file string, start, end int) string {
	if !issueLinkSegments(job.Repository, false) || !issueLinkSegments(file, true) || start < 0 || end < 0 || (end > 0 && end < start) {
		return ""
	}
	base, err := issueLinkBase(job.APIBaseURL, job.Provider == domain.ProviderGitLab && policy.AllowGitLabHTTP)
	if err != nil {
		return ""
	}
	marker := ""
	switch job.Provider {
	case domain.ProviderGitHub:
		if len(strings.Split(job.Repository, "/")) != 2 {
			return ""
		}
		if strings.EqualFold(base.Host, "api.github.com") && base.Path == "" {
			base.Host = "github.com"
		} else {
			base.Path = strings.TrimSuffix(base.Path, "/api/v3")
		}
		if policy.GitHubPublicBaseURL != "" && strings.TrimSuffix(policy.GitHubPublicForAPIBaseURL, "/") == strings.TrimSuffix(job.APIBaseURL, "/") {
			base, err = issueLinkBase(policy.GitHubPublicBaseURL, false)
			if err != nil {
				return ""
			}
		}
		marker = "/blob/HEAD/"
	case domain.ProviderGitLab:
		if !strings.HasSuffix(base.Path, "/api/v4") {
			return ""
		}
		base.Path = strings.TrimSuffix(base.Path, "/api/v4")
		if policy.GitLabPublicBaseURL != "" && strings.TrimSuffix(policy.GitLabPublicForAPIBaseURL, "/") == strings.TrimSuffix(job.APIBaseURL, "/") {
			base, err = issueLinkBase(policy.GitLabPublicBaseURL, policy.AllowGitLabHTTP)
			if err != nil || (base.Scheme == "http" && !issueLinkLoopback(base.Hostname())) {
				return ""
			}
		}
		// Never publish an internal development/container origin as a browser
		// link when the deployment has not paired it with a public origin.
		if base.Scheme == "http" && !issueLinkLoopback(base.Hostname()) {
			return ""
		}
		marker = "/-/blob/HEAD/"
	default:
		return ""
	}
	base.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + "/" + issueLinkEscape(job.Repository) + marker + issueLinkEscape(file)
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + job.Repository + marker + file
	if start > 0 {
		base.Fragment = "L" + strconv.Itoa(start)
		if end > start {
			if job.Provider == domain.ProviderGitHub {
				base.Fragment += "-L"
			} else {
				base.Fragment += "-"
			}
			base.Fragment += strconv.Itoa(end)
		}
	}
	return base.String()
}
