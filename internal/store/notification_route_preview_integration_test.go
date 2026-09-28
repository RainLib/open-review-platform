package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestNotificationRoutePreviewUsesStableFirstMatchAndNeverQueuesDelivery(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID := uuid.New()
	tenantSlug := "notification-preview-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Notification preview')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	selected, err := postgres.CreateNotificationDestination(ctx, "owner", tenantSlug, domain.NotificationDestinationInput{
		Name: "Selected", Provider: domain.NotificationSlack, SecretRef: "env:NOTIFICATION_PREVIEW_SELECTED", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create selected destination: %v", err)
	}
	threshold, err := postgres.CreateNotificationDestination(ctx, "owner", tenantSlug, domain.NotificationDestinationInput{
		Name: "High threshold", Provider: domain.NotificationFeishu, SecretRef: "env:NOTIFICATION_PREVIEW_THRESHOLD", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create threshold destination: %v", err)
	}
	paused, err := postgres.CreateNotificationDestination(ctx, "owner", tenantSlug, domain.NotificationDestinationInput{
		Name: "Paused target", Provider: domain.NotificationDingTalk, SecretRef: "env:NOTIFICATION_PREVIEW_PAUSED", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create paused destination: %v", err)
	}

	createRoute := func(destinationID uuid.UUID, minimum string) domain.NotificationRoute {
		t.Helper()
		route, err := postgres.CreateNotificationRoute(ctx, "owner", tenantSlug, domain.NotificationRouteInput{
			DestinationID: destinationID, RepositoryGlob: "RainLib/*", BranchGlob: "main", EventTypes: []string{"review.run.completed"}, MinSeverity: minimum, Enabled: true,
		})
		if err != nil {
			t.Fatalf("create notification route: %v", err)
		}
		return route
	}
	first := createRoute(selected.ID, "low")
	second := createRoute(selected.ID, "low")
	third := createRoute(threshold.ID, "high")
	fourth := createRoute(paused.ID, "low")
	if first.Priority != 1 || second.Priority != 2 || third.Priority != 3 || fourth.Priority != 4 {
		t.Fatalf("created priorities = %d, %d, %d, %d; want 1..4", first.Priority, second.Priority, third.Priority, fourth.Priority)
	}

	preview, err := postgres.PreviewNotificationRoutes(ctx, "owner", tenantSlug, domain.NotificationRoutePreviewInput{
		Repository: "RainLib/open-review-platform", TargetBranch: "main", EventType: "review.run.completed", HighestSeverity: "medium", FindingCount: 1,
	})
	if err != nil {
		t.Fatalf("preview notification routes: %v", err)
	}
	if len(preview.Matches) != 4 {
		t.Fatalf("matches=%#v, want four route decisions", preview.Matches)
	}
	want := []struct {
		routeID       uuid.UUID
		disposition   string
		destinationID uuid.UUID
	}{
		{first.ID, "selected", selected.ID},
		{second.ID, "deduplicated", selected.ID},
		{third.ID, "filtered", threshold.ID},
		{fourth.ID, "filtered", paused.ID},
	}
	for index, expected := range want {
		actual := preview.Matches[index]
		if actual.RouteID != expected.routeID || actual.DestinationID != expected.destinationID || actual.Disposition != expected.disposition {
			t.Fatalf("match %d=%#v, want route=%s destination=%s disposition=%s", index, actual, expected.routeID, expected.destinationID, expected.disposition)
		}
	}
	if preview.Matches[2].Reason != "highest severity medium is below the high threshold" || preview.Matches[3].Reason != "destination is paused" {
		t.Fatalf("unexpected filtered explanations: %#v", preview.Matches)
	}

	reordered, err := postgres.ReorderNotificationRoutes(ctx, "owner", tenantSlug, domain.NotificationRouteReorderInput{Routes: []domain.NotificationRouteOrderItem{
		{ID: second.ID, ExpectedRevision: second.Revision},
		{ID: first.ID, ExpectedRevision: first.Revision},
		{ID: third.ID, ExpectedRevision: third.Revision},
		{ID: fourth.ID, ExpectedRevision: fourth.Revision},
	}})
	if err != nil {
		t.Fatalf("reorder notification routes: %v", err)
	}
	if len(reordered) != 4 || reordered[0].ID != second.ID || reordered[0].Priority != 1 || reordered[1].ID != first.ID || reordered[1].Priority != 2 || reordered[0].Revision != second.Revision+1 {
		t.Fatalf("unexpected reordered routes: %#v", reordered)
	}
	preview, err = postgres.PreviewNotificationRoutes(ctx, "owner", tenantSlug, domain.NotificationRoutePreviewInput{
		Repository: "RainLib/open-review-platform", TargetBranch: "main", EventType: "review.run.completed", HighestSeverity: "medium", FindingCount: 1,
	})
	if err != nil {
		t.Fatalf("preview reordered notification routes: %v", err)
	}
	if preview.Matches[0].RouteID != second.ID || preview.Matches[0].Disposition != "selected" || preview.Matches[1].RouteID != first.ID || preview.Matches[1].Disposition != "deduplicated" {
		t.Fatalf("reordered preview does not use explicit priority: %#v", preview.Matches)
	}
	if _, err := postgres.ReorderNotificationRoutes(ctx, "owner", tenantSlug, domain.NotificationRouteReorderInput{Routes: []domain.NotificationRouteOrderItem{
		{ID: second.ID, ExpectedRevision: second.Revision},
		{ID: first.ID, ExpectedRevision: first.Revision},
		{ID: third.ID, ExpectedRevision: reordered[2].Revision},
		{ID: fourth.ID, ExpectedRevision: reordered[3].Revision},
	}}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale notification route reorder error = %v, want ErrRevisionConflict", err)
	}
	var queued int
	if err := postgres.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM outbox_messages outbox
		JOIN review_runs run ON run.id = outbox.aggregate_id
		JOIN review_requests request ON request.id = run.request_id
		WHERE outbox.aggregate_type = 'review_run' AND request.tenant_id = $1`, tenantID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("route preview must not queue a review event, found %d", queued)
	}
}
