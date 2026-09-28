package domain

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// AgentDraftURLPolicy contains only deployment-owned provider origins. The
// task's provider/API/repository and the provider's PR number remain immutable
// inputs checked separately by Canonical.
type AgentDraftURLPolicy struct {
	GitHubPublicBaseURL       string
	GitHubPublicForAPIBaseURL string
	GitLabPublicBaseURL       string
	GitLabPublicForAPIBaseURL string
	AllowGitLabHTTP           bool
}

func (policy AgentDraftURLPolicy) Canonical(provider Provider, apiBaseURL, repository string, number int, rawURL string) (string, error) {
	switch provider {
	case ProviderGitHub:
		return CanonicalGitHubDraftURL(apiBaseURL, repository, number, rawURL, policy.GitHubPublicBaseURL, policy.GitHubPublicForAPIBaseURL)
	case ProviderGitLab:
		return CanonicalGitLabDraftURL(apiBaseURL, repository, number, rawURL, policy.GitLabPublicBaseURL, policy.GitLabPublicForAPIBaseURL, policy.AllowGitLabHTTP)
	default:
		return "", fmt.Errorf("agent draft provider is unsupported")
	}
}

// File binds a Draft report's changed-file link to its exact pushed commit.
// The web origin comes only from the admitted API base or its deployment-owned
// public pairing; a repository path can never select another host.
func (policy AgentDraftURLPolicy) File(provider Provider, apiBaseURL, repository, headSHA, filePath string) (string, error) {
	if !validAgentDraftSegments(filePath, true) {
		return "", fmt.Errorf("agent Draft file identity is invalid")
	}
	web, marker, err := policy.draftCommitWebBase(provider, apiBaseURL, repository, headSHA)
	if err != nil {
		return "", err
	}
	web.RawPath = strings.TrimSuffix(web.EscapedPath(), "/") + "/" + escapeAgentDraftSegments(repository) + marker + "blob/" + headSHA + "/" + escapeAgentDraftSegments(filePath)
	web.Path = strings.TrimSuffix(web.Path, "/") + "/" + repository + marker + "blob/" + headSHA + "/" + filePath
	return web.String(), nil
}

// Commit is the stable fallback for a deleted file: a head-tree blob URL
// would return 404, while the exact pushed commit still contains its diff.
func (policy AgentDraftURLPolicy) Commit(provider Provider, apiBaseURL, repository, headSHA string) (string, error) {
	web, marker, err := policy.draftCommitWebBase(provider, apiBaseURL, repository, headSHA)
	if err != nil {
		return "", err
	}
	web.RawPath = strings.TrimSuffix(web.EscapedPath(), "/") + "/" + escapeAgentDraftSegments(repository) + marker + "commit/" + headSHA
	web.Path = strings.TrimSuffix(web.Path, "/") + "/" + repository + marker + "commit/" + headSHA
	return web.String(), nil
}

func (policy AgentDraftURLPolicy) draftCommitWebBase(provider Provider, apiBaseURL, repository, headSHA string) (*url.URL, string, error) {
	if !validAgentDraftSegments(repository, false) || (len(headSHA) != 40 && len(headSHA) != 64) {
		return nil, "", fmt.Errorf("agent Draft commit identity is invalid")
	}
	for _, digit := range headSHA {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return nil, "", fmt.Errorf("agent Draft commit revision is invalid")
		}
	}
	var web *url.URL
	var marker string
	switch provider {
	case ProviderGitHub:
		if len(strings.Split(repository, "/")) != 2 {
			return nil, "", fmt.Errorf("GitHub Draft repository is invalid")
		}
		api, err := parseAgentDraftBase(apiBaseURL, false)
		if err != nil {
			return nil, "", err
		}
		copy := *api
		if api.Host == "api.github.com" && api.Path == "" {
			copy.Host = "github.com"
		} else {
			copy.Path = strings.TrimSuffix(api.Path, "/api/v3")
		}
		web = &copy
		if strings.TrimSpace(policy.GitHubPublicBaseURL) != "" && strings.TrimSuffix(strings.TrimSpace(policy.GitHubPublicForAPIBaseURL), "/") == strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/") {
			web, err = parseAgentDraftBase(policy.GitHubPublicBaseURL, false)
			if err != nil {
				return nil, "", err
			}
		}
		marker = "/"
	case ProviderGitLab:
		api, err := parseAgentDraftBase(apiBaseURL, policy.AllowGitLabHTTP)
		if err != nil || !strings.HasSuffix(api.Path, "/api/v4") {
			return nil, "", fmt.Errorf("GitLab Draft API base is invalid")
		}
		copy := *api
		copy.Path = strings.TrimSuffix(api.Path, "/api/v4")
		web = &copy
		if strings.TrimSpace(policy.GitLabPublicBaseURL) != "" && strings.TrimSuffix(strings.TrimSpace(policy.GitLabPublicForAPIBaseURL), "/") == strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/") {
			web, err = parseAgentDraftBase(policy.GitLabPublicBaseURL, policy.AllowGitLabHTTP)
			if err != nil {
				return nil, "", err
			}
		}
		marker = "/-/"
	default:
		return nil, "", fmt.Errorf("agent Draft provider is unsupported")
	}
	return web, marker, nil
}

func validAgentDraftSegments(value string, allowSpaces bool) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\x00\r\n\t") {
			return false
		}
		if !allowSpaces && strings.ContainsAny(segment, " ?#") {
			return false
		}
	}
	return true
}

func escapeAgentDraftSegments(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// CanonicalGitLabDraftURL accepts a provider MR link only for the admitted
// repository and IID. An optional deployment-owned public base translates the
// Docker/private API origin into a browser-reachable link, but only for the
// exact configured API base. Other GitLab installations retain their own URL.
// Neither the model nor the adapter callback gets to choose that public origin.
func CanonicalGitLabDraftURL(apiBaseURL, repository string, iid int, rawURL, publicBaseURL, publicForAPIBaseURL string, allowHTTP bool) (string, error) {
	if iid < 1 || strings.Trim(repository, "/") != repository || repository == "" {
		return "", fmt.Errorf("GitLab draft identity is invalid")
	}
	for _, segment := range strings.Split(repository, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "?#\\\t\r\n") {
			return "", fmt.Errorf("GitLab draft repository is invalid")
		}
	}
	api, err := parseAgentDraftBase(apiBaseURL, allowHTTP)
	if err != nil || !strings.HasSuffix(api.Path, "/api/v4") {
		return "", fmt.Errorf("GitLab API base is invalid")
	}
	internal := *api
	internal.Path = strings.TrimSuffix(api.Path, "/api/v4")
	public := &internal
	if strings.TrimSpace(publicBaseURL) != "" && strings.TrimSuffix(strings.TrimSpace(publicForAPIBaseURL), "/") == strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/") {
		public, err = parseAgentDraftBase(publicBaseURL, allowHTTP)
		if err != nil {
			return "", fmt.Errorf("GitLab public base is invalid")
		}
	}
	provided, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || provided == nil || provided.Opaque != "" || provided.User != nil || provided.RawQuery != "" || provided.Fragment != "" {
		return "", fmt.Errorf("GitLab draft URL is invalid")
	}
	suffix := "/" + repository + "/-/merge_requests/" + strconv.Itoa(iid)
	if !agentDraftMatchesBase(provided, &internal, suffix) && !agentDraftMatchesBase(provided, public, suffix) {
		return "", fmt.Errorf("GitLab draft URL does not match the admitted repository and MR")
	}
	canonical := *public
	canonical.Path = strings.TrimSuffix(public.Path, "/") + suffix
	canonical.RawPath = ""
	return canonical.String(), nil
}

// CanonicalGitHubDraftURL binds a reported PR link to the frozen repository,
// number and deployment-owned web origin. A GitHub Enterprise deployment may
// use a distinct public web base, paired with one exact API base.
func CanonicalGitHubDraftURL(apiBaseURL, repository string, number int, rawURL, publicBaseURL, publicForAPIBaseURL string) (string, error) {
	parts := strings.Split(repository, "/")
	if number < 1 || len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(repository, "?#\\\t\r\n ") || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", fmt.Errorf("GitHub draft identity is invalid")
	}
	api, err := parseAgentDraftBase(apiBaseURL, false)
	if err != nil {
		return "", fmt.Errorf("GitHub API base is invalid")
	}
	web := *api
	if api.Host == "api.github.com" && api.Path == "" {
		web.Host = "github.com"
	} else {
		web.Path = strings.TrimSuffix(api.Path, "/api/v3")
	}
	if strings.TrimSpace(publicBaseURL) != "" && strings.TrimSuffix(strings.TrimSpace(publicForAPIBaseURL), "/") == strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/") {
		public, parseErr := parseAgentDraftBase(publicBaseURL, false)
		if parseErr != nil {
			return "", fmt.Errorf("GitHub public base is invalid")
		}
		web = *public
	}
	provided, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || provided == nil || provided.Scheme != "https" || provided.Host == "" || provided.Opaque != "" || provided.User != nil || provided.RawQuery != "" || provided.Fragment != "" || provided.RawPath != "" {
		return "", fmt.Errorf("GitHub draft URL is invalid")
	}
	suffix := "/" + repository + "/pull/" + strconv.Itoa(number)
	if provided.Scheme != web.Scheme || provided.Host != web.Host || !strings.EqualFold(provided.Path, strings.TrimSuffix(web.Path, "/")+suffix) {
		return "", fmt.Errorf("GitHub draft URL does not match the admitted repository and PR")
	}
	canonical := web
	canonical.Path = strings.TrimSuffix(web.Path, "/") + suffix
	canonical.RawPath = ""
	return canonical.String(), nil
}

func parseAgentDraftBase(value string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(value), "/"))
	if err != nil || parsed == nil || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) || strings.ContainsAny(parsed.Path, "()\t\r\n ") {
		return nil, fmt.Errorf("draft base URL is invalid")
	}
	return parsed, nil
}

func agentDraftMatchesBase(value, base *url.URL, suffix string) bool {
	return value.Scheme == base.Scheme && value.Host == base.Host && value.Path == strings.TrimSuffix(base.Path, "/")+suffix && value.RawPath == "" && value.RawQuery == "" && value.Fragment == ""
}
