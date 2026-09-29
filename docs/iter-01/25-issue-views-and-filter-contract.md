# 25 — Issues saved views and grouped filters

The approved Issues workbench in [14](14-console-v2-design-plan.md) and the
`FilterBuilder` contract in [15](15-luminous-spatial-design-system.md) require
saved views and composable filters. This fills that existing interaction
contract; it does not introduce a new visual direction or turn this inbox
into a mirror of provider-authored GitHub/GitLab Issues.

## User journey

```text
Built-in view / saved view
  → field + operator + value, AND/OR groups
  → Apply: URL filter intent → authenticated BFF → tenant-scoped SQL
  → Save: personal or workspace definition + revision
  → refresh / reopen → same definition, newly evaluated relative time
  → rename / update / delete → permission + revision check + audit
```

The existing Light/Dark surfaces, compact inbox, inspector and keyboard
navigation remain. Saved-view controls belong beside the inbox controls;
complex editing is progressively disclosed rather than permanently expanding
the list. Invalid query links show a recoverable validation error, not an empty
result. A saved view changed by unsaved URL filters is visibly marked changed.

## Filter contract

`filters` is URL-encoded JSON with a group at its root:

```json
{
  "condition": "and",
  "items": [
    { "field": "provider", "operator": "is", "value": "gitlab" },
    {
      "condition": "or",
      "items": [
        { "field": "severity", "operator": "is", "value": "critical" },
        { "field": "severity", "operator": "is", "value": "high" }
      ]
    },
    { "field": "age", "operator": "within", "value": "7d" }
  ]
}
```

- Fields: status, severity, category, repository, provider, API instance, path,
  assignee, retained rule attribution, search text and last-seen age.
- Enumerations use `is` / `is_not`; text also supports `contains` /
  `not_contains`; age supports `within` / `not_within` for 24h, 7d or 30d.
- Assignee `me` resolves at query time to the authenticated reader. Saving a
  shared view never embeds its creator's subject as the meaning of “me”.
- Root group + one nested group + leaves: maximum depth 3, 20 predicates,
  8 KiB JSON and 512 UTF-8 bytes per value. Unknown fields/operators, NUL,
  mixed group/predicate objects and invalid empty groups are rejected. Only
  the root empty AND is valid, represented as `{"condition":"and","items":[]}`.
- SQL column names come from an allowlist. Values are bound parameters; `%`,
  `_` and backslash in contains filters are literal text. The tenant predicate
  stays outside the parenthesized expression, including every OR branch.
- Repository-name filtering may intentionally span multiple instances. Add
  provider and API instance conditions to select one installation identity.
- Rule filtering uses retained occurrence/finding attribution, not model text
  that merely mentions a rule. No unverified attribution is inferred.

Built-in views retain their existing semantics. `all` adds no status predicate;
Open, Resolved and the others constrain the grouped expression. Combining
contradictory conditions is a valid zero-match query, not permission to ignore
one of the conditions.

## Persistence and API

| Method | Route suffix under `/v1/tenants/{slug}` | Contract |
| --- | --- | --- |
| GET | `/issue-views` | All visible personal/shared views, with `can_manage` |
| POST | `/issue-views` | Name, visibility, definition; returns revision 1 |
| PUT | `/issue-views/{id}` | Name, visibility, definition, expected revision |
| DELETE | `/issue-views/{id}?revision=N` | Deletes only the authorized current revision |
| GET | `/issues?filters=...&filter_time=...` | Actual filtered results/count and cursor anchor |

A definition contains only `{view, filters}`. Saving normalizes existing quick
filters/search into that expression. It must never persist a selected row,
pagination cursor, absolute pagination anchor, access token or actor identity.
Applying a different view clears those navigation-only values. Relative age is
re-evaluated when opening a saved view; paging keeps the returned `filter_time`.

| Scope | Read | Create/update/delete |
| --- | --- | --- |
| Personal | Creating member only | That member only |
| Workspace | Active workspace members | Owner/admin only |

Authorization is enforced on every API/store operation, not just hidden UI
buttons. Tenant membership removal also removes access to saved views. Private
views cannot be discovered through an ID belonging to another member or
workspace. Updates/deletes use optimistic revisions; stale writes and duplicate
names return conflict rather than overwriting a newer configuration. Limits
are 100 personal views per member and 100 shared views per workspace, with no
silent list truncation.

## Counting and pagination

The list's `total_count` uses the complete effective filter, independent of the
page size. Built-in tab badges keep their workspace-view counts so another tab
does not misleadingly appear empty because the current tab is filtered.
Keyset cursors bind the base view, filter expression and time anchor; changing
filters while reusing a cursor is rejected. This is not a frozen database
snapshot: issue updates can change ordering while a user is paging. Reloading
re-evaluates current data rather than promising repeatable-read history.

## Acceptance evidence

Implementation acceptance requires all of the following; merely defining these
checks does not establish that they passed:

1. Domain and API rejection tests for invalid/mixed/deep/oversized expressions.
2. Real PostgreSQL tests for grouped SQL, rule attribution, literal LIKE
   characters, same-name repositories, counts, bidirectional pagination and
   changed-filter cursor rejection.
3. Real PostgreSQL personal/shared/tenant isolation, membership, duplicate,
   concurrency/revision and audit tests.
4. Frontend serialization and navigation tests covering complete quick-filter
   preservation and clearing stale cursor/selection/time.
5. Authenticated browser: compose groups, apply, save, refresh, reopen, rename,
   dirty-state indication, error recovery, keyboard focus and Light/Dark.
6. `scripts/verify-issue-views.mjs` exercises the live BFF/control API/database
   after deployment. It requires an explicit acceptance workspace and loopback
   preview origin; it removes only its own newly created test view and never
   calls the provider or model. A one-row runtime dataset does not prove
   multi-page behavior; that needs the isolated database tests above.

Current run outcomes are retained in the
[core completion matrix](24-core-flow-completion-matrix.md).
