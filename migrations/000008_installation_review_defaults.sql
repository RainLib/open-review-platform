-- Review defaults belong to the installation boundary. They are safe to show
-- in the console and deliberately exclude credentials or provider tokens.
ALTER TABLE provider_installations
    ADD COLUMN IF NOT EXISTS automatic_reviews BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS minimum_severity TEXT NOT NULL DEFAULT 'medium'
        CHECK (minimum_severity IN ('low', 'medium', 'high', 'critical'));
