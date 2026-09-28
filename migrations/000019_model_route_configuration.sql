-- Models/BYOK is a seventh versioned review configuration section. The
-- credential remains an opaque reference; review snapshots never contain a
-- secret value.
ALTER TABLE review_configurations
    DROP CONSTRAINT IF EXISTS review_configurations_section_check;
ALTER TABLE review_configurations
    ADD CONSTRAINT review_configurations_section_check
    CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models'));

ALTER TABLE review_configuration_snapshots
    DROP CONSTRAINT IF EXISTS review_configuration_snapshots_section_check;
ALTER TABLE review_configuration_snapshots
    ADD CONSTRAINT review_configuration_snapshots_section_check
    CHECK (section IN ('general', 'categories', 'filters', 'prompts', 'summary', 'messages', 'models'));
