package providerchecks

import (
	"context"
	"fmt"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/providertransport"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var diagnosticSecret = regexp.MustCompile(`(?i)(?:bearer\s+[a-z0-9_.+/=-]+|(?:token|secret|password|api[_-]?key)\s*[:=]\s*[^\s,;]+|gh[pousr]_[a-z0-9_]+|github_pat_[a-z0-9_]+|sk-[a-z0-9_-]+)`)
var diagnosticANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// Unknown failures require operator diagnosis. A terminal CI failure alone
// does not authorize a coding repair of runner, credentials or networking.
func classifyDiagnostics(raw, token string) (string, string) {
	if len(raw) > 12000 || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return "", "unknown"
	}
	raw = strings.ReplaceAll(raw, token, "[redacted]")
	raw = diagnosticANSI.ReplaceAllString(raw, "")
	raw = diagnosticSecret.ReplaceAllString(raw, "[redacted]")
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	for _, signal := range []string{"runner system failure", "no runners", "no space left", "connection refused", "network is unreachable", "could not resolve host", "rate limit", "permission denied", "authentication failed", "out of memory", "infrastructure failure", "failed to pull image"} {
		if strings.Contains(lower, signal) {
			return raw, "infrastructure"
		}
	}
	for _, signal := range []string{"--- fail:", "assertionerror", "assertion failed", "expected:", "test failed", "tests failed", "error ts", "syntaxerror", "undefined:", "cannot find module", "compilation failed", "lint error"} {
		if strings.Contains(lower, signal) {
			return raw, "code"
		}
	}
	return raw, "unknown"
}

func (c Client) getText(ctx context.Context, endpoint, token string, provider domain.Provider) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := c.HTTPClient
	if client == nil {
		client = providertransport.NewClient(c.AllowPrivateNetworks)
	}
	res, err := httpguard.NoRedirects(client, 20*time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("CI trace unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 12001))
	if err != nil || len(raw) > 12000 {
		return "", fmt.Errorf("CI trace exceeds diagnostic budget")
	}
	return string(raw), nil
}
