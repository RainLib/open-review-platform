package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var terminalNotificationTopics = map[string]domain.RunState{
	"review.run.completed":       domain.RunCompleted,
	"review.run.failed":          domain.RunFailed,
	"review.run.cancelled":       domain.RunCancelled,
	"review.run.superseded":      domain.RunSuperseded,
	"review.run.needs_attention": domain.RunNeedsAttention,
}

const notificationTestTopic = "notification.destination.test"

func (s *PostgresStore) CreateNotificationDestination(ctx context.Context, actor, tenantSlug string, input domain.NotificationDestinationInput) (domain.NotificationDestination, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.SecretRef = strings.TrimSpace(input.SecretRef)
	if input.Name == "" || len(input.Name) > 120 || !input.Provider.Valid() || !validSecretRef(input.SecretRef) {
		return domain.NotificationDestination{}, ErrInvalidNotification
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationDestination{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.NotificationDestination{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("begin notification destination creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result domain.NotificationDestination
	err = tx.QueryRow(ctx, `
		INSERT INTO notification_destinations (tenant_id, name, provider, secret_ref, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, name, provider, secret_ref, enabled, revision, created_at, updated_at`, tenantID, input.Name, input.Provider, input.SecretRef, input.Enabled).
		Scan(&result.ID, &result.TenantID, &result.Name, &result.Provider, &result.SecretRef, &result.Enabled, &result.Revision, &result.CreatedAt, &result.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.NotificationDestination{}, ErrConflict
	}
	if err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("create notification destination: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'notification_destination.created', $3, jsonb_build_object('provider', $4::text))`, tenantID, actor, result.ID.String(), string(result.Provider)); err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("audit notification destination creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("commit notification destination creation: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListNotificationDestinations(ctx context.Context, actor, tenantSlug string) ([]domain.NotificationDestination, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, tenant_id, name, provider, secret_ref, enabled, revision, created_at, updated_at FROM notification_destinations WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list notification destinations: %w", err)
	}
	defer rows.Close()
	items := make([]domain.NotificationDestination, 0)
	for rows.Next() {
		var item domain.NotificationDestination
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.Provider, &item.SecretRef, &item.Enabled, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification destination: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// RequestNotificationTest records the administrator's intent and atomically
// hands it to the outbox. It deliberately does not resolve the credential or
// perform network I/O in the API request.
func (s *PostgresStore) RequestNotificationTest(ctx context.Context, actor, tenantSlug string, destinationID uuid.UUID, input domain.NotificationTestInput) (domain.NotificationDeliverySummary, error) {
	if destinationID == uuid.Nil || input.ExpectedRevision < 1 {
		return domain.NotificationDeliverySummary{}, ErrInvalidNotification
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("begin notification test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationDeliverySummary{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.NotificationDeliverySummary{}, ErrForbidden
	}

	var destination domain.NotificationDestination
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, name, provider, secret_ref, enabled, revision, created_at, updated_at
		FROM notification_destinations
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE`, destinationID, tenantID).
		Scan(&destination.ID, &destination.TenantID, &destination.Name, &destination.Provider, &destination.SecretRef, &destination.Enabled, &destination.Revision, &destination.CreatedAt, &destination.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationDeliverySummary{}, ErrNotFound
	}
	if err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("load notification destination for test: %w", err)
	}
	if destination.Revision != input.ExpectedRevision {
		return domain.NotificationDeliverySummary{}, ErrRevisionConflict
	}
	if !destination.Enabled {
		return domain.NotificationDeliverySummary{}, ErrConflict
	}

	eventID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (id, aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ($1::uuid, 'notification_destination', $2::uuid, 'notification.destination.test', $3,
		        jsonb_build_object('destination_id', ($2::uuid)::text, 'destination_revision', $4::integer))`,
		eventID, destination.ID, "notification-test:"+eventID.String(), destination.Revision); err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("queue notification test: %w", err)
	}

	var result domain.NotificationDeliverySummary
	err = tx.QueryRow(ctx, `
		INSERT INTO notification_deliveries (event_id, run_id, destination_id)
		VALUES ($1, NULL, $2)
		RETURNING id, state, attempt, created_at, updated_at`, eventID.String(), destination.ID).
		Scan(&result.ID, &result.State, &result.Attempt, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("create notification test receipt: %w", err)
	}
	result.EventID = eventID.String()
	result.EventType = notificationTestTopic
	result.DestinationID = destination.ID
	result.DestinationName = destination.Name
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'notification_destination.test_requested', $3,
		        jsonb_build_object('event_id', $4::text, 'revision', $5::integer))`,
		tenantID, actor, destination.ID.String(), eventID.String(), destination.Revision); err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("audit notification test request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NotificationDeliverySummary{}, fmt.Errorf("commit notification test request: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) CreateNotificationRoute(ctx context.Context, actor, tenantSlug string, input domain.NotificationRouteInput) (domain.NotificationRoute, error) {
	input.RepositoryGlob = strings.TrimSpace(input.RepositoryGlob)
	input.BranchGlob = strings.TrimSpace(input.BranchGlob)
	if input.BranchGlob == "" {
		input.BranchGlob = "*"
	}
	input.MinSeverity = strings.ToLower(strings.TrimSpace(input.MinSeverity))
	if input.DestinationID == uuid.Nil || input.RepositoryGlob == "" || !validRouteGlob(input.RepositoryGlob) || !validRouteGlob(input.BranchGlob) || !validSeverity(input.MinSeverity) || !validNotificationEvents(input.EventTypes) {
		return domain.NotificationRoute{}, ErrInvalidNotification
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.NotificationRoute{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("begin notification route creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id = $1 FOR UPDATE`, tenantID); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("lock notification route tenant: %w", err)
	}
	var priority int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(priority), 0) + 1 FROM notification_routes WHERE tenant_id = $1`, tenantID).Scan(&priority); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("allocate notification route priority: %w", err)
	}
	var result domain.NotificationRoute
	err = tx.QueryRow(ctx, `
		INSERT INTO notification_routes (tenant_id, destination_id, repository_glob, branch_glob, event_types, min_severity, enabled, priority)
		SELECT $1, id, $3, $4, $5, $6, $7, $8 FROM notification_destinations WHERE id = $2 AND tenant_id = $1
		RETURNING id, tenant_id, destination_id, repository_glob, branch_glob, event_types, min_severity, enabled, priority, revision, created_at, updated_at`, tenantID, input.DestinationID, input.RepositoryGlob, input.BranchGlob, input.EventTypes, input.MinSeverity, input.Enabled, priority).
		Scan(&result.ID, &result.TenantID, &result.DestinationID, &result.RepositoryGlob, &result.BranchGlob, &result.EventTypes, &result.MinSeverity, &result.Enabled, &result.Priority, &result.Revision, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationRoute{}, ErrNotFound
	}
	if err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("create notification route: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'notification_route.created', $3, jsonb_build_object('destination_id', $4::text, 'repository_glob', $5::text, 'branch_glob', $6::text, 'min_severity', $7::text, 'priority', $8::integer))`, tenantID, actor, result.ID.String(), result.DestinationID.String(), result.RepositoryGlob, result.BranchGlob, result.MinSeverity, result.Priority); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("audit notification route creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("commit notification route creation: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListNotificationRoutes(ctx context.Context, actor, tenantSlug string) ([]domain.NotificationRoute, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, tenant_id, destination_id, repository_glob, branch_glob, event_types, min_severity, enabled, priority, revision, created_at, updated_at FROM notification_routes WHERE tenant_id = $1 ORDER BY priority, id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list notification routes: %w", err)
	}
	defer rows.Close()
	items := make([]domain.NotificationRoute, 0)
	for rows.Next() {
		var item domain.NotificationRoute
		if err := rows.Scan(&item.ID, &item.TenantID, &item.DestinationID, &item.RepositoryGlob, &item.BranchGlob, &item.EventTypes, &item.MinSeverity, &item.Enabled, &item.Priority, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification route: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PreviewNotificationRoutes evaluates the exact matching and de-duplication
// rules that PrepareNotificationDeliveries uses, without creating an outbox
// message or resolving a destination credential.
func (s *PostgresStore) PreviewNotificationRoutes(ctx context.Context, actor, tenantSlug string, input domain.NotificationRoutePreviewInput) (domain.NotificationRoutePreview, error) {
	input.Repository = strings.TrimSpace(input.Repository)
	input.TargetBranch = strings.TrimSpace(input.TargetBranch)
	input.EventType = strings.TrimSpace(input.EventType)
	input.HighestSeverity = strings.ToLower(strings.TrimSpace(input.HighestSeverity))
	if !validNotificationRoutePreview(input) {
		return domain.NotificationRoutePreview{}, ErrInvalidNotification
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationRoutePreview{}, err
	}
	event := domain.NotificationEvent{
		Type:            input.EventType,
		Repository:      input.Repository,
		TargetBranch:    input.TargetBranch,
		HighestSeverity: input.HighestSeverity,
		FindingCount:    input.FindingCount,
	}
	rows, err := s.pool.Query(ctx, `
		SELECT route.id, route.destination_id, route.repository_glob, route.branch_glob, route.event_types, route.min_severity, route.enabled, route.priority,
		       destination.name, destination.enabled
		FROM notification_routes route
		JOIN notification_destinations destination ON destination.id = route.destination_id
		WHERE route.tenant_id = $1
		ORDER BY route.priority, route.id`, tenantID)
	if err != nil {
		return domain.NotificationRoutePreview{}, fmt.Errorf("list notification routes for preview: %w", err)
	}
	defer rows.Close()

	preview := domain.NotificationRoutePreview{Input: input, Matches: make([]domain.NotificationRoutePreviewMatch, 0)}
	selectedDestinations := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var route domain.NotificationRoute
		var destination domain.NotificationDestination
		if err := rows.Scan(&route.ID, &route.DestinationID, &route.RepositoryGlob, &route.BranchGlob, &route.EventTypes, &route.MinSeverity, &route.Enabled, &route.Priority, &destination.Name, &destination.Enabled); err != nil {
			return domain.NotificationRoutePreview{}, fmt.Errorf("scan notification route preview: %w", err)
		}
		match := domain.NotificationRoutePreviewMatch{
			RouteID:         route.ID,
			DestinationID:   route.DestinationID,
			DestinationName: destination.Name,
		}
		match.Disposition, match.Reason = notificationRoutePreviewDecision(route, destination, event, selectedDestinations)
		preview.Matches = append(preview.Matches, match)
	}
	if err := rows.Err(); err != nil {
		return domain.NotificationRoutePreview{}, err
	}
	return preview, nil
}

func (s *PostgresStore) UpdateNotificationDestination(ctx context.Context, actor, tenantSlug string, destinationID uuid.UUID, input domain.NotificationDestinationUpdateInput) (domain.NotificationDestination, error) {
	if destinationID == uuid.Nil || input.ExpectedRevision < 1 || (input.Name == nil && input.SecretRef == nil && input.Enabled == nil) {
		return domain.NotificationDestination{}, ErrInvalidNotification
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value == "" || len(value) > 120 {
			return domain.NotificationDestination{}, ErrInvalidNotification
		}
		input.Name = &value
	}
	if input.SecretRef != nil {
		value := strings.TrimSpace(*input.SecretRef)
		if !validSecretRef(value) {
			return domain.NotificationDestination{}, ErrInvalidNotification
		}
		input.SecretRef = &value
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("begin notification destination update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationDestination{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.NotificationDestination{}, ErrForbidden
	}
	var result domain.NotificationDestination
	err = tx.QueryRow(ctx, `
		UPDATE notification_destinations
		SET name = COALESCE($4, name), secret_ref = COALESCE($5, secret_ref), enabled = COALESCE($6, enabled), revision = revision + 1, updated_at = now()
		WHERE id = $1 AND tenant_id = $2 AND revision = $3
		RETURNING id, tenant_id, name, provider, secret_ref, enabled, revision, created_at, updated_at`, destinationID, tenantID, input.ExpectedRevision, input.Name, input.SecretRef, input.Enabled).
		Scan(&result.ID, &result.TenantID, &result.Name, &result.Provider, &result.SecretRef, &result.Enabled, &result.Revision, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationDestination{}, notificationMutationMiss(ctx, tx, tenantID, "notification_destinations", destinationID)
	}
	if isUniqueViolation(err) {
		return domain.NotificationDestination{}, ErrConflict
	}
	if err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("update notification destination: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'notification_destination.updated', $3, jsonb_build_object('revision', $4::integer, 'enabled', $5::boolean))`, tenantID, actor, result.ID.String(), result.Revision, result.Enabled); err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("audit notification destination update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NotificationDestination{}, fmt.Errorf("commit notification destination update: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) UpdateNotificationRoute(ctx context.Context, actor, tenantSlug string, routeID uuid.UUID, input domain.NotificationRouteUpdateInput) (domain.NotificationRoute, error) {
	if routeID == uuid.Nil || input.ExpectedRevision < 1 || (input.RepositoryGlob == nil && input.BranchGlob == nil && input.EventTypes == nil && input.MinSeverity == nil && input.Enabled == nil) {
		return domain.NotificationRoute{}, ErrInvalidNotification
	}
	if input.RepositoryGlob != nil {
		value := strings.TrimSpace(*input.RepositoryGlob)
		if !validRouteGlob(value) {
			return domain.NotificationRoute{}, ErrInvalidNotification
		}
		input.RepositoryGlob = &value
	}
	if input.BranchGlob != nil {
		value := strings.TrimSpace(*input.BranchGlob)
		if !validRouteGlob(value) {
			return domain.NotificationRoute{}, ErrInvalidNotification
		}
		input.BranchGlob = &value
	}
	if input.EventTypes != nil && !validNotificationEvents(*input.EventTypes) {
		return domain.NotificationRoute{}, ErrInvalidNotification
	}
	if input.MinSeverity != nil {
		value := strings.ToLower(strings.TrimSpace(*input.MinSeverity))
		if !validSeverity(value) {
			return domain.NotificationRoute{}, ErrInvalidNotification
		}
		input.MinSeverity = &value
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("begin notification route update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.NotificationRoute{}, ErrForbidden
	}
	var result domain.NotificationRoute
	err = tx.QueryRow(ctx, `
		UPDATE notification_routes
		SET repository_glob = COALESCE($4, repository_glob), branch_glob = COALESCE($5, branch_glob), event_types = COALESCE($6, event_types), min_severity = COALESCE($7, min_severity), enabled = COALESCE($8, enabled), revision = revision + 1, updated_at = now()
		WHERE id = $1 AND tenant_id = $2 AND revision = $3
		RETURNING id, tenant_id, destination_id, repository_glob, branch_glob, event_types, min_severity, enabled, priority, revision, created_at, updated_at`, routeID, tenantID, input.ExpectedRevision, input.RepositoryGlob, input.BranchGlob, input.EventTypes, input.MinSeverity, input.Enabled).
		Scan(&result.ID, &result.TenantID, &result.DestinationID, &result.RepositoryGlob, &result.BranchGlob, &result.EventTypes, &result.MinSeverity, &result.Enabled, &result.Priority, &result.Revision, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationRoute{}, notificationMutationMiss(ctx, tx, tenantID, "notification_routes", routeID)
	}
	if err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("update notification route: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'notification_route.updated', $3, jsonb_build_object('revision', $4::integer, 'enabled', $5::boolean))`, tenantID, actor, result.ID.String(), result.Revision, result.Enabled); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("audit notification route update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("commit notification route update: %w", err)
	}
	return result, nil
}

// ReorderNotificationRoutes replaces the complete, tenant-local first-match
// order. The publisher and dry-run preview both query this same priority, so
// administrators can resolve overlapping routing intentionally rather than
// relying on accidental creation time.
func (s *PostgresStore) ReorderNotificationRoutes(ctx context.Context, actor, tenantSlug string, input domain.NotificationRouteReorderInput) ([]domain.NotificationRoute, error) {
	if len(input.Routes) < 1 || len(input.Routes) > 200 {
		return nil, ErrInvalidNotification
	}
	expected := make(map[uuid.UUID]int, len(input.Routes))
	for _, item := range input.Routes {
		if item.ID == uuid.Nil || item.ExpectedRevision < 1 {
			return nil, ErrInvalidNotification
		}
		if _, duplicate := expected[item.ID]; duplicate {
			return nil, ErrInvalidNotification
		}
		expected[item.ID] = item.ExpectedRevision
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin notification route reorder: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id = $1 FOR UPDATE`, tenantID); err != nil {
		return nil, fmt.Errorf("lock notification route reorder tenant: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, destination_id, repository_glob, branch_glob, event_types, min_severity, enabled, priority, revision, created_at, updated_at
		FROM notification_routes
		WHERE tenant_id = $1
		ORDER BY priority, id
		FOR UPDATE`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("lock notification routes for reorder: %w", err)
	}
	current := make([]domain.NotificationRoute, 0, len(input.Routes))
	for rows.Next() {
		var route domain.NotificationRoute
		if err := rows.Scan(&route.ID, &route.TenantID, &route.DestinationID, &route.RepositoryGlob, &route.BranchGlob, &route.EventTypes, &route.MinSeverity, &route.Enabled, &route.Priority, &route.Revision, &route.CreatedAt, &route.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan notification route reorder: %w", err)
		}
		current = append(current, route)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate notification route reorder: %w", err)
	}
	rows.Close()
	if len(current) != len(input.Routes) {
		return nil, ErrRevisionConflict
	}
	unchanged := true
	for index, route := range current {
		if expectedRevision, ok := expected[route.ID]; !ok || expectedRevision != route.Revision {
			return nil, ErrRevisionConflict
		}
		if input.Routes[index].ID != route.ID {
			unchanged = false
		}
	}
	if unchanged {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit unchanged notification route order: %w", err)
		}
		return current, nil
	}

	// Move the whole set into a disjoint negative range before assigning the
	// requested positive ranks. This preserves the unique tenant priority
	// invariant even while two adjacent routes exchange positions.
	if _, err := tx.Exec(ctx, `UPDATE notification_routes SET priority = -priority WHERE tenant_id = $1`, tenantID); err != nil {
		return nil, fmt.Errorf("stage notification route reorder: %w", err)
	}
	for index, item := range input.Routes {
		if _, err := tx.Exec(ctx, `
			UPDATE notification_routes
			SET priority = $3, revision = revision + 1, updated_at = now()
			WHERE id = $1 AND tenant_id = $2`, item.ID, tenantID, index+1); err != nil {
			return nil, fmt.Errorf("apply notification route reorder: %w", err)
		}
	}
	routeIDs := make([]string, 0, len(input.Routes))
	for _, item := range input.Routes {
		routeIDs = append(routeIDs, item.ID.String())
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'notification_route.reordered', 'notification-routes',
		        jsonb_build_object('route_ids', $3::text[], 'count', $4::integer))`, tenantID, actor, routeIDs, len(routeIDs)); err != nil {
		return nil, fmt.Errorf("audit notification route reorder: %w", err)
	}
	byID := make(map[uuid.UUID]domain.NotificationRoute, len(current))
	for _, route := range current {
		byID[route.ID] = route
	}
	ordered := make([]domain.NotificationRoute, 0, len(input.Routes))
	for index, item := range input.Routes {
		route := byID[item.ID]
		route.Priority = index + 1
		route.Revision++
		ordered = append(ordered, route)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit notification route reorder: %w", err)
	}
	return ordered, nil
}

func (s *PostgresStore) ListNotificationDeliveries(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.NotificationDeliverySummary, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidNotification
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT delivery.id, delivery.event_id, COALESCE(outbox.topic, 'unknown'), COALESCE(delivery.run_id::text, ''), delivery.destination_id, destination.name,
		       delivery.state, delivery.attempt, delivery.response_code, COALESCE(delivery.last_error, ''), delivery.delivered_at, delivery.created_at, delivery.updated_at
		FROM notification_deliveries delivery
		JOIN notification_destinations destination ON destination.id = delivery.destination_id
		LEFT JOIN outbox_messages outbox ON outbox.id::text = delivery.event_id
		WHERE destination.tenant_id = $1
		ORDER BY delivery.created_at DESC, delivery.id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification deliveries: %w", err)
	}
	defer rows.Close()
	items := make([]domain.NotificationDeliverySummary, 0)
	for rows.Next() {
		var item domain.NotificationDeliverySummary
		var runID string
		if err := rows.Scan(&item.ID, &item.EventID, &item.EventType, &runID, &item.DestinationID, &item.DestinationName, &item.State, &item.Attempt, &item.ResponseCode, &item.LastError, &item.DeliveredAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification delivery: %w", err)
		}
		if runID != "" {
			parsed, err := uuid.Parse(runID)
			if err != nil {
				return nil, fmt.Errorf("parse notification delivery run id: %w", err)
			}
			item.RunID = &parsed
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) RetryNotificationDelivery(ctx context.Context, actor, tenantSlug string, deliveryID uuid.UUID) error {
	if deliveryID == uuid.Nil {
		return ErrInvalidNotification
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin notification retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	var runID, destinationID uuid.UUID
	var topic string
	err = tx.QueryRow(ctx, `
		SELECT delivery.run_id, delivery.destination_id, outbox.topic
		FROM notification_deliveries delivery
		JOIN notification_destinations destination ON destination.id = delivery.destination_id AND destination.tenant_id = $2 AND destination.enabled = TRUE
		JOIN outbox_messages outbox ON outbox.id::text = delivery.event_id
		WHERE delivery.id = $1 AND delivery.state = 'failed'`, deliveryID, tenantID).Scan(&runID, &destinationID, &topic)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("load notification retry: %w", err)
	}
	if _, ok := terminalNotificationTopics[topic]; !ok {
		return ErrConflict
	}
	retryID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (id, aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ($1, 'review_run', $2, $3, $4, jsonb_build_object('run_id', ($2::uuid)::text, 'target_destination_id', ($5::uuid)::text, 'retry_of', ($6::uuid)::text))`, retryID, runID, topic, "notification-retry:"+retryID.String(), destinationID, deliveryID); err != nil {
		return fmt.Errorf("queue notification retry: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'notification_delivery.retried', $3, jsonb_build_object('retry_event_id', $4::text))`, tenantID, actor, deliveryID.String(), retryID.String()); err != nil {
		return fmt.Errorf("audit notification retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit notification retry: %w", err)
	}
	return nil
}

func notificationMutationMiss(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, table string, resourceID uuid.UUID) error {
	query := "SELECT EXISTS(SELECT 1 FROM " + table + " WHERE id = $1 AND tenant_id = $2)"
	var exists bool
	if err := tx.QueryRow(ctx, query, resourceID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("resolve notification mutation conflict: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrRevisionConflict
}

func (s *PostgresStore) PrepareNotificationDeliveries(ctx context.Context, message domain.OutboxMessage) ([]domain.NotificationDelivery, error) {
	if message.Topic == notificationTestTopic {
		return s.prepareNotificationTestDelivery(ctx, message)
	}
	state, ok := terminalNotificationTopics[message.Topic]
	if !ok {
		return nil, fmt.Errorf("unsupported notification topic %q", message.Topic)
	}
	event := domain.NotificationEvent{ID: message.ID.String(), Type: message.Topic, RunID: message.AggregateID, State: state, HighestSeverity: "none"}
	err := s.pool.QueryRow(ctx, `
		SELECT run.revision, request.repository, request.review_number, request.provider, request.api_base_url, run.head_sha, COALESCE(job.base_ref, ''),
		       COUNT(finding.id), COALESCE((ARRAY_AGG(finding.severity ORDER BY CASE finding.severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END DESC))[1], 'none')
		FROM review_runs run
		JOIN review_requests request ON request.id = run.request_id
		LEFT JOIN review_jobs job ON job.id = run.legacy_job_id
		LEFT JOIN review_findings finding ON finding.job_id = job.id
		WHERE run.id = $1
		GROUP BY run.revision, request.repository, request.review_number, request.provider, request.api_base_url, run.head_sha, job.base_ref`, message.AggregateID).
		Scan(&event.Revision, &event.Repository, &event.ReviewNumber, &event.Provider, &event.APIBaseURL, &event.HeadSHA, &event.TargetBranch, &event.FindingCount, &event.HighestSeverity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load notification event: %w", err)
	}
	event.ReviewURL = providerReviewURL(event.Provider, event.APIBaseURL, event.Repository, event.ReviewNumber)
	var targetDestinationID *uuid.UUID
	if value, ok := message.Payload["target_destination_id"].(string); ok && value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("target notification destination: %w", err)
		}
		targetDestinationID = &parsed
	}
	var rows pgx.Rows
	if targetDestinationID != nil {
		// An administrator explicitly retries one failed destination. The
		// original delivery already proves that a route matched, so current route
		// edits must not silently turn an accepted retry into a no-op.
		rows, err = s.pool.Query(ctx, `
			SELECT destination.id, destination.tenant_id, destination.name, destination.provider, destination.secret_ref, destination.enabled, destination.revision, destination.created_at, destination.updated_at
			FROM review_runs run
			JOIN review_requests request ON request.id = run.request_id
			JOIN notification_destinations destination ON destination.tenant_id = request.tenant_id AND destination.id = $2 AND destination.enabled = TRUE
			WHERE run.id = $1`, message.AggregateID, *targetDestinationID)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT destination.id, destination.tenant_id, destination.name, destination.provider, destination.secret_ref, destination.enabled, destination.revision, destination.created_at, destination.updated_at,
			       route.id, route.repository_glob, route.branch_glob, route.event_types, route.min_severity, route.enabled, route.priority
			FROM review_runs run
			JOIN review_requests request ON request.id = run.request_id
			JOIN notification_routes route ON route.tenant_id = request.tenant_id AND route.enabled = TRUE AND $2 = ANY(route.event_types)
			JOIN notification_destinations destination ON destination.id = route.destination_id AND destination.enabled = TRUE
			WHERE run.id = $1
			ORDER BY route.priority, route.id`, message.AggregateID, message.Topic)
	}
	if err != nil {
		return nil, fmt.Errorf("load notification routes: %w", err)
	}
	defer rows.Close()
	deliveries := make([]domain.NotificationDelivery, 0)
	seenDestinations := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var destination domain.NotificationDestination
		var route domain.NotificationRoute
		if targetDestinationID != nil {
			if err := rows.Scan(&destination.ID, &destination.TenantID, &destination.Name, &destination.Provider, &destination.SecretRef, &destination.Enabled, &destination.Revision, &destination.CreatedAt, &destination.UpdatedAt); err != nil {
				return nil, fmt.Errorf("scan notification retry destination: %w", err)
			}
		} else if err := rows.Scan(&destination.ID, &destination.TenantID, &destination.Name, &destination.Provider, &destination.SecretRef, &destination.Enabled, &destination.Revision, &destination.CreatedAt, &destination.UpdatedAt, &route.ID, &route.RepositoryGlob, &route.BranchGlob, &route.EventTypes, &route.MinSeverity, &route.Enabled, &route.Priority); err != nil {
			return nil, fmt.Errorf("scan notification route: %w", err)
		}
		if targetDestinationID == nil {
			if matched, _ := notificationRouteMatches(route, destination, event); !matched {
				continue
			}
		}
		if !destination.Enabled {
			continue
		}
		if _, seen := seenDestinations[destination.ID]; seen {
			continue
		}
		seenDestinations[destination.ID] = struct{}{}
		var delivery domain.NotificationDelivery
		delivery.Event, delivery.Destination = event, destination
		err := s.pool.QueryRow(ctx, `
			INSERT INTO notification_deliveries (event_id, run_id, destination_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (event_id, destination_id) DO UPDATE SET updated_at = now()
			WHERE notification_deliveries.state <> 'delivered'
			RETURNING id, attempt`, event.ID, event.RunID, destination.ID).Scan(&delivery.ID, &delivery.Attempt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("prepare notification delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// prepareNotificationTestDelivery reads the test receipt created at admission.
// Tests bypass routing intentionally, but never bypass destination revision or
// enabled-state checks. A stale test is retained as a failed receipt rather
// than silently sending with a configuration the requester did not review.
func (s *PostgresStore) prepareNotificationTestDelivery(ctx context.Context, message domain.OutboxMessage) ([]domain.NotificationDelivery, error) {
	destinationID, expectedRevision, err := notificationTestPayload(message.Payload)
	if err != nil {
		return nil, err
	}
	var delivery domain.NotificationDelivery
	err = s.pool.QueryRow(ctx, `
		SELECT delivery.id, delivery.attempt,
		       destination.id, destination.tenant_id, destination.name, destination.provider, destination.secret_ref,
		       destination.enabled, destination.revision, destination.created_at, destination.updated_at
		FROM notification_deliveries delivery
		JOIN notification_destinations destination ON destination.id = delivery.destination_id
		WHERE delivery.event_id = $1 AND delivery.destination_id = $2
		  AND delivery.state IN ('pending', 'failed')`, message.ID.String(), destinationID).
		Scan(&delivery.ID, &delivery.Attempt,
			&delivery.Destination.ID, &delivery.Destination.TenantID, &delivery.Destination.Name, &delivery.Destination.Provider, &delivery.Destination.SecretRef,
			&delivery.Destination.Enabled, &delivery.Destination.Revision, &delivery.Destination.CreatedAt, &delivery.Destination.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// A completed test is safe to suppress on broker redelivery. A deleted
		// destination cascades its receipt and must not be recreated implicitly.
		return []domain.NotificationDelivery{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load notification test receipt: %w", err)
	}
	if delivery.Destination.Revision != expectedRevision || !delivery.Destination.Enabled {
		reason := "notification destination changed before test delivery"
		if !delivery.Destination.Enabled {
			reason = "notification destination was paused before test delivery"
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE notification_deliveries
			SET state = 'failed', attempt = attempt + 1, response_code = NULL, last_error = $2, updated_at = now()
			WHERE id = $1`, delivery.ID, reason); err != nil {
			return nil, fmt.Errorf("record stale notification test receipt: %w", err)
		}
		return []domain.NotificationDelivery{}, nil
	}
	delivery.Event = domain.NotificationEvent{
		ID:   message.ID.String(),
		Type: notificationTestTopic,
		Test: true,
	}
	return []domain.NotificationDelivery{delivery}, nil
}

func notificationTestPayload(payload map[string]any) (uuid.UUID, int, error) {
	value, ok := payload["destination_id"].(string)
	if !ok || value == "" {
		return uuid.Nil, 0, fmt.Errorf("notification test destination is required")
	}
	destinationID, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("notification test destination: %w", err)
	}
	var revision int
	switch value := payload["destination_revision"].(type) {
	case int:
		revision = value
	case int32:
		revision = int(value)
	case int64:
		revision = int(value)
	case float64:
		if value != float64(int(value)) {
			return uuid.Nil, 0, fmt.Errorf("notification test destination revision is required")
		}
		revision = int(value)
	default:
		return uuid.Nil, 0, fmt.Errorf("notification test destination revision is required")
	}
	if revision < 1 {
		return uuid.Nil, 0, fmt.Errorf("notification test destination revision is required")
	}
	return destinationID, revision, nil
}

func (s *PostgresStore) FinishNotificationDelivery(ctx context.Context, deliveryID uuid.UUID, status int, sendErr error) error {
	state, summary := "delivered", ""
	if sendErr != nil {
		state, summary = "failed", safeNotificationError(sendErr)
	}
	_, err := s.pool.Exec(ctx, `UPDATE notification_deliveries SET state = $2, attempt = attempt + 1, response_code = NULLIF($3, 0), last_error = NULLIF($4, ''), delivered_at = CASE WHEN $2 = 'delivered' THEN now() ELSE NULL END, updated_at = now() WHERE id = $1`, deliveryID, state, status, summary)
	return err
}

func validSecretRef(value string) bool {
	if len(value) > 132 || !strings.HasPrefix(value, "env:") {
		return false
	}
	name := strings.TrimPrefix(value, "env:")
	if name == "" {
		return false
	}
	for _, char := range name {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validSeverity(value string) bool { return severityRank(value) > 0 }

func severityRank(value string) int {
	switch strings.ToLower(value) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func validNotificationEvents(events []string) bool {
	if len(events) == 0 || len(events) > len(terminalNotificationTopics) {
		return false
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if _, ok := terminalNotificationTopics[event]; !ok {
			return false
		}
		if _, duplicate := seen[event]; duplicate {
			return false
		}
		seen[event] = struct{}{}
	}
	return true
}

func validNotificationRoutePreview(input domain.NotificationRoutePreviewInput) bool {
	if input.Repository == "" || len(input.Repository) > 300 || input.TargetBranch == "" || len(input.TargetBranch) > 300 || input.FindingCount < 0 {
		return false
	}
	if strings.ContainsAny(input.Repository, "*?#[]") || strings.ContainsAny(input.TargetBranch, "*?#[]") {
		return false
	}
	if _, ok := terminalNotificationTopics[input.EventType]; !ok {
		return false
	}
	return (input.FindingCount == 0 && (input.HighestSeverity == "" || input.HighestSeverity == "none")) || (input.FindingCount > 0 && validSeverity(input.HighestSeverity))
}

func notificationRouteMatches(route domain.NotificationRoute, destination domain.NotificationDestination, event domain.NotificationEvent) (bool, string) {
	if !route.Enabled {
		return false, "route is paused"
	}
	if !destination.Enabled {
		return false, "destination is paused"
	}
	if !notificationEventSelected(route.EventTypes, event.Type) {
		return false, "event type is not selected by this route"
	}
	if !routeGlobMatches(route.RepositoryGlob, event.Repository) {
		return false, "repository does not match the route scope"
	}
	if !routeGlobMatches(route.BranchGlob, event.TargetBranch) {
		return false, "target branch does not match the route scope"
	}
	if event.FindingCount > 0 && severityRank(event.HighestSeverity) < severityRank(route.MinSeverity) {
		return false, fmt.Sprintf("highest severity %s is below the %s threshold", event.HighestSeverity, route.MinSeverity)
	}
	return true, notificationRouteMatchReason(event)
}

func notificationRoutePreviewDecision(route domain.NotificationRoute, destination domain.NotificationDestination, event domain.NotificationEvent, selectedDestinations map[uuid.UUID]struct{}) (string, string) {
	if matched, reason := notificationRouteMatches(route, destination, event); !matched {
		return "filtered", reason
	}
	if _, selected := selectedDestinations[route.DestinationID]; selected {
		return "deduplicated", "an earlier matching route already selected this destination"
	}
	selectedDestinations[route.DestinationID] = struct{}{}
	return "selected", notificationRouteMatchReason(event)
}

func notificationRouteMatchReason(event domain.NotificationEvent) string {
	if event.FindingCount == 0 {
		return "event and scope match; no finding threshold applies"
	}
	return "event, repository, branch, and severity match"
}

func notificationEventSelected(events []string, eventType string) bool {
	for _, candidate := range events {
		if candidate == eventType {
			return true
		}
	}
	return false
}

func routeGlobMatches(pattern, value string) bool {
	pattern, value = strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(strings.TrimSpace(value))
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == value
}

func validRouteGlob(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 300 && !strings.ContainsAny(value, "?#[]") && strings.Count(value, "*") <= 1 && (!strings.Contains(value, "*") || strings.HasSuffix(value, "*"))
}

func safeNotificationError(err error) string {
	value := strings.TrimSpace(err.Error())
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func providerReviewURL(provider domain.Provider, apiBaseURL, repository string, reviewNumber int) string {
	parsed, err := url.Parse(apiBaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	switch provider {
	case domain.ProviderGitHub:
		if parsed.Host == "api.github.com" {
			parsed.Host = "github.com"
			parsed.Path = ""
		} else {
			parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v3")
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + strings.Trim(repository, "/") + "/pull/" + fmt.Sprint(reviewNumber)
	case domain.ProviderGitLab:
		parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v4") + "/" + strings.Trim(repository, "/") + "/-/merge_requests/" + fmt.Sprint(reviewNumber)
	default:
		return ""
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	return parsed.String()
}
