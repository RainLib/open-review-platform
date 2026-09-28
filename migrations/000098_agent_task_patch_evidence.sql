-- Persist bounded, source-free evidence for the exact patch validated before
-- an Agent task creates its Draft PR/MR. Older completed attempts remain
-- distinguishable by empty/zero evidence fields.
ALTER TABLE agent_task_attempts
    ADD COLUMN patch_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN changed_file_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN diff_bytes BIGINT NOT NULL DEFAULT 0;

ALTER TABLE agent_task_attempts
    ADD CONSTRAINT agent_task_attempts_patch_evidence_check CHECK (
        (patch_sha256 = '' AND changed_file_count = 0 AND diff_bytes = 0)
        OR (patch_sha256 ~ '^[0-9a-f]{64}$' AND changed_file_count > 0 AND diff_bytes > 0)
    );
