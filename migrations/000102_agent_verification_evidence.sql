-- A successful, deployment-approved verification command is a bounded
-- attempt-specific fact, not a product-acceptance or provider-publication fact.
-- Empty fields preserve the distinction for old or unverified attempts.
ALTER TABLE agent_task_attempts
    ADD COLUMN verification_profile_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN verification_output_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN verification_output_bytes BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT agent_task_attempts_verification_evidence_check CHECK (
        (verification_profile_sha256 = '' AND verification_output_sha256 = '' AND verification_output_bytes = 0)
        OR (verification_profile_sha256 ~ '^[0-9a-f]{64}$'
            AND verification_output_sha256 ~ '^[0-9a-f]{64}$'
            AND verification_output_bytes BETWEEN 0 AND 1048576)
    );

ALTER TABLE agent_task_publication_checkpoints
    ADD COLUMN verification_profile_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN verification_output_sha256 TEXT NOT NULL DEFAULT '',
    ADD COLUMN verification_output_bytes BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT agent_task_publication_verification_evidence_check CHECK (
        (verification_profile_sha256 = '' AND verification_output_sha256 = '' AND verification_output_bytes = 0)
        OR (verification_profile_sha256 ~ '^[0-9a-f]{64}$'
            AND verification_output_sha256 ~ '^[0-9a-f]{64}$'
            AND verification_output_bytes BETWEEN 0 AND 1048576)
    );
