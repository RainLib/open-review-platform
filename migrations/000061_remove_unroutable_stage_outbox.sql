-- Intermediate review stages are retained in review_run_events and streamed
-- from PostgreSQL. They have no AMQP consumer and mandatory publishing returns
-- them as NO_ROUTE forever, so remove only still-pending poison rows left by
-- older binaries. Terminal/admission/acknowledgement messages remain durable.
DELETE FROM outbox_messages
WHERE published_at IS NULL
  AND topic IN (
    'review.run.preparing',
    'review.run.analyzing',
    'review.run.normalizing',
    'review.run.publishing'
  );
