-- Stage rows existed before transitions updated them. Reconstruct the best
-- durable state available from the run and immutable event ledger. The
-- backfill is conservative: terminal failures mark only the furthest reached
-- stage failed, while later stages are skipped rather than reported as run.
WITH stage_rank AS (
    SELECT * FROM (VALUES
        ('ack', 1), ('admit', 2), ('prepare', 3),
        ('analyze', 4), ('normalize', 5), ('publish', 6)
    ) AS value(stage, rank)
),
run_progress AS (
    SELECT
        run.id,
        run.state,
        run.created_at,
        run.started_at,
        run.finished_at,
        GREATEST(
            CASE run.state
                WHEN 'acknowledged' THEN 1 WHEN 'admitted' THEN 2
                WHEN 'preparing' THEN 3 WHEN 'analyzing' THEN 4
                WHEN 'normalizing' THEN 5 WHEN 'publishing' THEN 6
                WHEN 'completed' THEN 6 ELSE 0
            END,
            COALESCE(MAX(CASE event.event_type
                WHEN 'run.acknowledged' THEN 1 WHEN 'run.admitted' THEN 2
                WHEN 'run.preparing' THEN 3 WHEN 'run.analyzing' THEN 4
                WHEN 'run.normalizing' THEN 5 WHEN 'run.publishing' THEN 6
                WHEN 'run.completed' THEN 6 ELSE 0
            END), 0)
        ) AS reached_rank
    FROM review_runs run
    LEFT JOIN review_run_events event ON event.run_id = run.id
    GROUP BY run.id
)
UPDATE review_run_stages stage
SET
    state = CASE
        WHEN progress.state = 'completed' THEN 'succeeded'
        WHEN rank.rank < progress.reached_rank THEN 'succeeded'
        WHEN rank.rank = progress.reached_rank
             AND progress.state IN ('acknowledged', 'preparing', 'analyzing', 'normalizing', 'publishing') THEN 'running'
        WHEN rank.rank = progress.reached_rank
             AND progress.state IN ('failed', 'needs_attention') THEN 'failed'
        ELSE 'skipped'
    END,
    started_at = COALESCE(stage.started_at, progress.started_at, progress.created_at),
    finished_at = CASE
        WHEN progress.state IN ('acknowledged', 'preparing', 'analyzing', 'normalizing', 'publishing')
             AND rank.rank = progress.reached_rank THEN NULL
        ELSE COALESCE(stage.finished_at, progress.finished_at, progress.started_at, progress.created_at)
    END,
    details = CASE
        WHEN progress.state IN ('failed', 'needs_attention', 'cancelled', 'superseded')
        THEN stage.details || jsonb_build_object('backfilled_from_terminal_state', progress.state)
        ELSE stage.details
    END
FROM run_progress progress, stage_rank rank
WHERE stage.run_id = progress.id
  AND rank.stage = stage.stage
  AND stage.state = 'pending'
  AND progress.reached_rank > 0;
