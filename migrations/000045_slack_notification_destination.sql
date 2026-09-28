ALTER TABLE notification_destinations
    DROP CONSTRAINT IF EXISTS notification_destinations_provider_check;

ALTER TABLE notification_destinations
    ADD CONSTRAINT notification_destinations_provider_check
    CHECK (provider IN ('dingtalk', 'feishu', 'slack', 'webhook'));
