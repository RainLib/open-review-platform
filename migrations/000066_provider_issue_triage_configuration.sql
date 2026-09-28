ALTER TABLE provider_issue_analysis_jobs
    ADD COLUMN IF NOT EXISTS issue_triage_config JSONB NOT NULL DEFAULT '{"enabled":true,"preset":"engineering","language":"inherit","required_issue_sections":["outcome","reproduction","expected_behavior","evidence","acceptance_criteria"],"response_sections":["assessment","missing_context","acceptance_criteria","risk","affected_areas","next_steps","provenance"],"collapse_secondary":true,"link_file_references":true,"reaction_feedback":true,"max_items_per_section":6,"custom_guidance":""}'::jsonb,
    ADD COLUMN IF NOT EXISTS issue_triage_config_sha256 TEXT NOT NULL DEFAULT '220a255f8d976c705da525c81575074b5c693afceb9f540ec9b0b45bbbb3fbf1';

COMMENT ON COLUMN provider_issue_analysis_jobs.issue_triage_config IS
    'Immutable workspace/repository Issue triage format resolved when this revision was admitted.';

COMMENT ON COLUMN provider_issue_analysis_jobs.issue_triage_config_sha256 IS
    'SHA-256 of the canonical Issue triage configuration retained for provenance.';
