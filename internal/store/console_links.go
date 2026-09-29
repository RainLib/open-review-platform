package store

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// tenantConsoleLink only uses the deployment-owned public Console origin and a
// tenant slug loaded from the database. A missing or malformed origin omits the
// optional link instead of exposing an internal service address in a comment.
func tenantConsoleLink(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, route string) string {
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM tenants WHERE id=$1`, tenantID).Scan(&slug); err != nil {
		return ""
	}
	return consoleLink(os.Getenv("OPEN_REVIEW_APP_URL"), slug, route)
}

func consoleLink(baseURL, tenantSlug, route string) string {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && base.Scheme != "http") || strings.TrimSpace(tenantSlug) == "" {
		return ""
	}
	target, err := url.Parse(route)
	if err != nil || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") || target.Host != "" || target.Scheme != "" {
		return ""
	}
	basePath := strings.TrimSuffix(base.Path, "/")
	baseEscapedPath := strings.TrimSuffix(base.EscapedPath(), "/")
	base.Path = basePath + "/" + tenantSlug + target.Path
	base.RawPath = baseEscapedPath + "/" + url.PathEscape(tenantSlug) + target.EscapedPath()
	base.RawQuery = target.RawQuery
	base.Fragment = target.Fragment
	return base.String()
}
