-- pull_request_count is the historical number of distinct pull requests that
-- contributed an occurrence. active_occurrence_count separately represents
-- current impact, so resolving an issue must not erase its provider history.
UPDATE review_issues issue
SET pull_request_count = aggregate.pull_request_count,
    updated_at = CASE
        WHEN issue.pull_request_count <> aggregate.pull_request_count THEN now()
        ELSE issue.updated_at
    END
FROM (
    SELECT issue_id, COUNT(DISTINCT review_number)::INTEGER AS pull_request_count
    FROM review_issue_occurrences
    GROUP BY issue_id
) aggregate
WHERE issue.id = aggregate.issue_id
  AND issue.pull_request_count <> aggregate.pull_request_count;
