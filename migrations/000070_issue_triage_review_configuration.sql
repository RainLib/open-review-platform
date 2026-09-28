-- Issue triage joined the versioned review-configuration domain after the
-- original section constraints were created. Keep the database allow-lists in
-- sync with the domain so tenant/repository saves and admission snapshots do
-- not fail after the control plane accepts a valid issue-triage document.
ALTER TABLE review_configurations
    DROP CONSTRAINT IF EXISTS review_configurations_section_check;
ALTER TABLE review_configurations
    ADD CONSTRAINT review_configurations_section_check
    CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models', 'issue-triage'));

ALTER TABLE review_configuration_snapshots
    DROP CONSTRAINT IF EXISTS review_configuration_snapshots_section_check;
ALTER TABLE review_configuration_snapshots
    ADD CONSTRAINT review_configuration_snapshots_section_check
    CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models', 'issue-triage'));

ALTER TABLE review_config_change_requests
    DROP CONSTRAINT IF EXISTS review_config_change_requests_section_check;
ALTER TABLE review_config_change_requests
    ADD CONSTRAINT review_config_change_requests_section_check
    CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models', 'issue-triage'));
