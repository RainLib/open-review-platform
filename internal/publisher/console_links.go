package publisher

import (
	"net/url"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// consoleLinks derives provider-comment links from deployment configuration
// and immutable job identity. It fails closed: absent or malformed public
// configuration simply produces no links rather than emitting an internal
// service address or a request-controlled redirect.
func consoleLinks(baseURL string, job domain.ReviewJob) (reviewURL, commandsURL string) {
	if job.ID == uuid.Nil || strings.TrimSpace(job.TenantSlug) == "" {
		return "", ""
	}
	base, ok := parseConsoleLinkBase(baseURL)
	if !ok {
		return "", ""
	}
	reviewURL = consoleRouteURL(base, job.TenantSlug, "/reviews/"+job.ID.String())
	commandsURL = consoleRouteURL(base, job.TenantSlug, "/review-commands")
	return reviewURL, commandsURL
}

func parseConsoleLinkBase(value string) (url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return url.URL{}, false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return url.URL{}, false
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawPath = ""
	return *parsed, true
}

func consoleRouteURL(base url.URL, tenantSlug, suffix string) string {
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimSpace(tenantSlug) + suffix
	base.RawPath = ""
	return base.String()
}
