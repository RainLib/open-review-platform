package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const modelProbeLease = 45 * time.Second

func (s *PostgresStore) ListModelProbes(ctx context.Context, actor, tenantSlug string, scope domain.ReviewConfigScope, limit int) ([]domain.ModelProbeReceipt, error) {
	scope, valid := scope.Normalize()
	if !valid || limit < 1 || limit > 100 {
		return nil, ErrInvalidReviewConfig
	}
	view, err := s.GetReviewConfig(ctx, actor, tenantSlug, domain.ReviewConfigModels, scope)
	if err != nil {
		return nil, err
	}
	if view.OriginScopeKind == "default" {
		return []domain.ModelProbeReceipt{}, nil
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, modelProbeSelect+`
        WHERE tenant_id=$1 AND scope_kind=$2 AND scope_ref=$3
          AND scope_provider=$4 AND scope_api_base_url=$5
        ORDER BY created_at DESC, id DESC LIMIT $6`, tenantID, view.OriginScopeKind, view.OriginScopeRef, view.OriginProvider, view.OriginAPIBaseURL, limit)
	if err != nil {
		return nil, fmt.Errorf("list model probe receipts: %w", err)
	}
	defer rows.Close()
	receipts := make([]domain.ModelProbeReceipt, 0)
	for rows.Next() {
		receipt, err := scanModelProbeReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("scan model probe receipt: %w", err)
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model probe receipts: %w", err)
	}
	return receipts, nil
}

func (s *PostgresStore) RequestModelProbe(ctx context.Context, actor, tenantSlug string, input domain.ModelProbeInput) (domain.ModelProbeReceipt, error) {
	scope, valid := (domain.ReviewConfigScope{Kind: input.ScopeKind, Ref: input.ScopeRef, Provider: input.ScopeProvider, APIBaseURL: input.ScopeAPIBaseURL}).Normalize()
	if !input.Acknowledged || !valid || input.ExpectedRevision < 1 {
		return domain.ModelProbeReceipt{}, ErrInvalidReviewConfig
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("begin model probe request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" {
		return domain.ModelProbeReceipt{}, ErrForbidden
	}
	if err := ensureRepositoryReviewConfigScope(ctx, tx, tenantID, scope); err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	route, origin, revision, hash, err := loadModelProbeRouteTx(ctx, tx, tenantID, scope)
	if err != nil {
		return domain.ModelProbeReceipt{}, err
	}
	if !route.Enabled || revision != input.ExpectedRevision {
		return domain.ModelProbeReceipt{}, ErrRevisionConflict
	}
	parsed, err := url.Parse(route.BaseURL)
	if err != nil || parsed.Host == "" {
		return domain.ModelProbeReceipt{}, ErrInvalidReviewConfig
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM model_probe_receipts WHERE tenant_id=$1 AND scope_kind=$2 AND scope_ref=$3 AND config_revision=$4 AND scope_provider=$5 AND scope_api_base_url=$6 AND state IN ('queued','running'))`, tenantID, origin.Kind, origin.Ref, revision, origin.Provider, origin.APIBaseURL).Scan(&active); err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("check active model probe: %w", err)
	}
	if active {
		return domain.ModelProbeReceipt{}, ErrConflict
	}
	content, err := json.Marshal(route)
	if err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("serialize model probe route: %w", err)
	}
	receipt, err := scanModelProbeReceipt(tx.QueryRow(ctx, `
        INSERT INTO model_probe_receipts (tenant_id,scope_kind,scope_ref,config_revision,content_sha256,provider,protocol,model,endpoint_host,route_content,state,requested_by,scope_provider,scope_api_base_url)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,'queued',$11,$12,$13)
        RETURNING `+modelProbeColumns,
		tenantID, origin.Kind, origin.Ref, revision, hash, route.Provider, route.Protocol, route.Model, parsed.Host, content, actor, origin.Provider, origin.APIBaseURL))
	if err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("create model probe receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'model.probe_requested',$3,jsonb_build_object('scope_kind',$4::text,'scope_ref',$5::text,'config_revision',$6::int,'content_sha256',$7::text,'scope_provider',$8::text,'scope_api_base_url',$9::text))`, tenantID, actor, receipt.ID.String(), origin.Kind, origin.Ref, revision, hash, origin.Provider, origin.APIBaseURL); err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("audit model probe request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ModelProbeReceipt{}, fmt.Errorf("commit model probe request: %w", err)
	}
	return receipt, nil
}

func (s *PostgresStore) ClaimModelProbe(ctx context.Context, workerID string) (*domain.ModelProbeTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, ErrInvalidReviewConfig
	}
	var target domain.ModelProbeTarget
	var rawRoute []byte
	err := s.pool.QueryRow(ctx, `
        WITH candidate AS (
            SELECT id FROM model_probe_receipts
            WHERE state='queued' OR (state='running' AND locked_until < now())
            ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
        ), claimed AS (
            UPDATE model_probe_receipts receipt
            SET state='running',attempt=attempt+1,worker_id=$1,locked_until=now()+$2::interval,
                started_at=COALESCE(started_at,now()),error_code=NULL,error_message=NULL
            FROM candidate WHERE receipt.id=candidate.id
            RETURNING receipt.id,receipt.route_content
        ) SELECT id,route_content FROM claimed`, workerID, modelProbeLease.String()).Scan(&target.ReceiptID, &rawRoute)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedModelProbe
	}
	if err != nil {
		return nil, fmt.Errorf("claim model probe: %w", err)
	}
	if err := json.Unmarshal(rawRoute, &target.Route); err != nil || !target.Route.Valid() || !target.Route.Enabled {
		return nil, fmt.Errorf("decode model probe route: %w", err)
	}
	return &target, nil
}

func (s *PostgresStore) CompleteModelProbe(ctx context.Context, receiptID uuid.UUID, workerID string, result domain.ModelProbeResult) error {
	if receiptID == uuid.Nil || strings.TrimSpace(workerID) == "" || result.LatencyMS < 0 {
		return ErrInvalidReviewConfig
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin model probe completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state := domain.ModelProbeSucceeded
	if result.ErrorCode != "" {
		state, result.ResponseSHA256 = domain.ModelProbeFailed, ""
	}
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
        UPDATE model_probe_receipts
        SET state=$3,response_sha256=NULLIF($4,''),latency_ms=$5,error_code=NULLIF($6,''),error_message=NULLIF($7,''),finished_at=now(),locked_until=NULL
        WHERE id=$1 AND worker_id=$2 AND state='running' AND locked_until >= now()
        RETURNING tenant_id`, receiptID, workerID, state, result.ResponseSHA256, result.LatencyMS, result.ErrorCode, truncateModelProbeError(result.ErrorMessage)).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrModelProbeClaimLost
	}
	if err != nil {
		return fmt.Errorf("complete model probe receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'model.probe_completed',$3,jsonb_build_object('state',$4::text,'error_code',$5::text,'latency_ms',$6::bigint))`, tenantID, "worker:"+workerID, receiptID.String(), state, result.ErrorCode, result.LatencyMS); err != nil {
		return fmt.Errorf("audit model probe completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit model probe completion: %w", err)
	}
	return nil
}

func loadModelProbeRouteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, requested domain.ReviewConfigScope) (domain.ModelRouteConfig, domain.ReviewConfigScope, int, string, error) {
	load := func(scope domain.ReviewConfigScope) (domain.ModelRouteConfig, domain.ReviewConfigScope, int, string, error) {
		var hash string
		var revision int
		var content []byte
		err := tx.QueryRow(ctx, `
            SELECT version.revision,version.content_sha256,version.content
            FROM review_configurations configuration
            JOIN LATERAL (SELECT revision,content_sha256,content FROM review_configuration_versions WHERE configuration_id=configuration.id ORDER BY revision DESC LIMIT 1) version ON TRUE
            WHERE configuration.tenant_id=$1 AND configuration.section='models' AND configuration.scope_kind=$2 AND configuration.scope_ref=$3 AND configuration.active=TRUE
              AND configuration.scope_provider=$4 AND configuration.scope_api_base_url=$5
            FOR UPDATE OF configuration`, tenantID, scope.Kind, scope.Ref, scope.Provider, scope.APIBaseURL).Scan(&revision, &hash, &content)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ModelRouteConfig{}, domain.ReviewConfigScope{}, 0, "", ErrNotFound
		}
		if err != nil {
			return domain.ModelRouteConfig{}, domain.ReviewConfigScope{}, 0, "", fmt.Errorf("load model probe route: %w", err)
		}
		route, err := domain.DecodeModelRoute(content)
		if err != nil {
			return domain.ModelRouteConfig{}, domain.ReviewConfigScope{}, 0, "", ErrInvalidReviewConfig
		}
		return route, scope, revision, hash, nil
	}
	route, origin, revision, hash, err := load(requested)
	if errors.Is(err, ErrNotFound) && requested.QualifiedRepository() {
		legacy := requested
		legacy.Provider, legacy.APIBaseURL = "", ""
		route, origin, revision, hash, err = load(legacy)
	}
	if errors.Is(err, ErrNotFound) && requested.Kind == domain.ReviewConfigRepositoryScope {
		return load(domain.ReviewConfigScope{Kind: domain.ReviewConfigTenantScope})
	}
	return route, origin, revision, hash, err
}

const modelProbeColumns = `id,scope_kind,scope_ref,config_revision,content_sha256,provider,protocol,model,endpoint_host,state,attempt,response_sha256,latency_ms,error_code,error_message,requested_by,created_at,started_at,finished_at,scope_provider,scope_api_base_url`
const modelProbeSelect = `SELECT ` + modelProbeColumns + ` FROM model_probe_receipts`

func scanModelProbeReceipt(row rowScanner) (domain.ModelProbeReceipt, error) {
	var receipt domain.ModelProbeReceipt
	var responseSHA, errorCode, errorMessage *string
	var latency *int64
	err := row.Scan(&receipt.ID, &receipt.ScopeKind, &receipt.ScopeRef, &receipt.ConfigRevision, &receipt.ContentSHA256, &receipt.Provider, &receipt.Protocol, &receipt.Model, &receipt.EndpointHost, &receipt.State, &receipt.Attempt, &responseSHA, &latency, &errorCode, &errorMessage, &receipt.RequestedBy, &receipt.CreatedAt, &receipt.StartedAt, &receipt.FinishedAt, &receipt.ScopeProvider, &receipt.ScopeAPIBaseURL)
	if responseSHA != nil {
		receipt.ResponseSHA256 = *responseSHA
	}
	if latency != nil {
		receipt.LatencyMS = *latency
	}
	if errorCode != nil {
		receipt.ErrorCode = *errorCode
	}
	if errorMessage != nil {
		receipt.ErrorMessage = *errorMessage
	}
	return receipt, err
}

func truncateModelProbeError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
