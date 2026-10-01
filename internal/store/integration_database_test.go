package store

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Queue tests whose contract requires there to be exactly one candidate need
// their own database; completed runs left by other fixtures are legitimate
// competing work, not a worker failure.
func isolatedQueueDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if base == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("queue isolation requires a disposable PostgreSQL server")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := "openreview_queue_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Errorf("clean isolated queue database: %v", err)
		}
		admin.Close(context.Background())
	})
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	target := parsed.String()
	if err = migrate.Apply(ctx, target, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return target
}
