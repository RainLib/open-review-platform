-- A notification test is a real asynchronous delivery receipt, but it is not
-- associated with a review run. Keeping it in the same delivery ledger means
-- operators see the provider result and redacted failure metadata in one place.
ALTER TABLE notification_deliveries
    ALTER COLUMN run_id DROP NOT NULL;
