-- A provider Issue created by automation remains part of the finding
-- lifecycle. Record provider-side closure explicitly so the console does not
-- present a resolved internal aggregate as an indefinitely open ticket.
ALTER TABLE external_issue_receipts
    DROP CONSTRAINT external_issue_receipts_state_check;

ALTER TABLE external_issue_receipts
    ADD CONSTRAINT external_issue_receipts_state_check
    CHECK (state IN ('queued', 'created', 'failed', 'cancelled', 'closed'));

-- Upgrade only the exact former built-in template. Workspace-authored
-- templates remain authoritative and are never overwritten by a migration.
UPDATE issue_auto_create_policies
SET title_template = '[Open Review] {{severity_upper}} {{category}} · {{path}}',
    body_template = $template$## 🔎 Finding summary

> **{{severity_upper}} · {{category}}** — action is required before this finding can be considered resolved.

| Context | Evidence |
| --- | --- |
| **Repository** | {{repository}} |
| **Location** | {{file_link}} |
| **Pull request** | {{review_link}} |
| **Occurrences** | **{{occurrence_count}}** |

### Why this matters

{{evidence}}

### Recommended fix

{{suggestion}}

<details>
<summary><strong>Traceability</strong></summary>

- Review run: `{{run_id}}`
- Reviewed commit: `{{head_sha}}`
- Internal issue: `{{issue_id}}`

</details>$template$,
    updated_at = now()
WHERE title_template = '[Open Review] {{severity}} {{category}} in {{path}}'
  AND body_template = E'Open Review detected a recurring {{severity}} {{category}} finding.\n\nRepository: {{repository}}\nPath: {{path}}\nFingerprint: {{fingerprint}}\nOccurrences: {{occurrence_count}}\n\n## Evidence\n{{evidence}}\n\n## Recommended direction\n{{suggestion}}';

-- Backfill the provider transition for aggregates that resolved before this
-- lifecycle stage existed. The revision-keyed dedupe key is the same contract
-- used by live resolution writes.
INSERT INTO outbox_messages (aggregate_type,aggregate_id,topic,dedupe_key,payload)
SELECT 'external_issue',receipt.id,'external.issue.close',
       'external-issue:' || receipt.id::text || ':close:' || issue.revision::text,
       jsonb_build_object('receipt_id',receipt.id::text)
FROM external_issue_receipts receipt
JOIN review_issues issue ON issue.id=receipt.issue_id AND issue.tenant_id=receipt.tenant_id
WHERE receipt.state='created' AND issue.status='resolved'
ON CONFLICT (dedupe_key) DO NOTHING;
