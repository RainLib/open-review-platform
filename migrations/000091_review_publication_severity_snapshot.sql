-- Keep the installation's publication floor immutable for each admitted run.
-- Existing runs retain NULL so replay does not silently change their behavior.
ALTER TABLE review_runs
    ADD COLUMN publication_minimum_severity TEXT
    CHECK (publication_minimum_severity IN ('low', 'medium', 'high', 'critical'));

CREATE OR REPLACE FUNCTION snapshot_review_publication_minimum()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.legacy_job_id IS NOT NULL AND NEW.publication_minimum_severity IS NULL THEN
        SELECT installation.minimum_severity
        INTO NEW.publication_minimum_severity
        FROM review_jobs job
        JOIN provider_installations installation ON installation.id = job.installation_id
        WHERE job.id = NEW.legacy_job_id;
        IF NEW.publication_minimum_severity IS NULL THEN
            RAISE EXCEPTION 'review run % has no installation publication policy', NEW.id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER review_runs_publication_minimum_snapshot
    BEFORE INSERT ON review_runs
    FOR EACH ROW EXECUTE FUNCTION snapshot_review_publication_minimum();
