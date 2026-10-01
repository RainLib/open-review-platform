ALTER TABLE agent_task_feedback_cycles
 DROP CONSTRAINT agent_task_feedback_cycles_source_review_run_id_key,
 ADD COLUMN internal_repair_kind TEXT NOT NULL DEFAULT '',
 ADD COLUMN internal_repair_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX agent_internal_repair_key ON agent_task_feedback_cycles(tenant_id,internal_repair_key) WHERE internal_repair_key<>'';
-- Legacy review bindings remain valid and deduplicated through the new key.
UPDATE agent_task_feedback_cycles SET internal_repair_key='review:'||source_review_run_id::text WHERE source_review_run_id IS NOT NULL;
ALTER TABLE agent_task_acceptances
 ADD COLUMN decision TEXT NOT NULL DEFAULT '' CHECK(decision IN ('','accepted','changes_requested')),
 ADD COLUMN recovery_reason TEXT NOT NULL DEFAULT '',
 ADD COLUMN decision_reason TEXT NOT NULL DEFAULT '',
 ADD COLUMN decision_revision INTEGER NOT NULL DEFAULT 0,
 ADD COLUMN provider_state TEXT NOT NULL DEFAULT '';
UPDATE agent_task_acceptances SET decision_reason=reason,decision_revision=revision WHERE decided_by<>'';
UPDATE agent_task_acceptances SET decision=CASE WHEN state IN ('accepted','changes_requested') AND decided_by<>'' THEN state ELSE '' END;

ALTER TABLE agent_task_requirements ADD COLUMN source_body TEXT NOT NULL DEFAULT '', ADD COLUMN repository_evidence TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_task_publication_checkpoints ADD COLUMN verification_criteria JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(verification_criteria)='array');
