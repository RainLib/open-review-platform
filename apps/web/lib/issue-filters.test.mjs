import assert from "node:assert/strict";
import { test } from "node:test";
import { emptyIssueFilters, issueDefinitionFromQuery, issueDefinitionHref, issueDefinitionKey, issueDisplaySummary, issueDisplayTitle, issueFilterAnchor, issueListPreview, parseIssueFilters, validateIssueFilters } from "./issue-filters.ts";

const condition = (field = "severity", value = "high", operator = "is") => ({ field, value, operator });
const group = (items = [], condition = "and") => ({ condition, items });

test("nested OR conditions round-trip with provider instance qualification", () => {
  const definition = { view: "all", filters: group([condition("provider", "gitlab"), condition("api_base_url", "http://gitlab.internal/api/v4"), group([condition("severity", "critical"), condition("severity", "high")], "or")]) };
  const url = new URL(issueDefinitionHref("my team", definition, "personal-view"), "http://console.test");
  assert.equal(url.pathname, "/my%20team/issues");
  assert.equal(url.searchParams.get("saved_view"), "personal-view");
  assert.deepEqual(issueDefinitionFromQuery(Object.fromEntries(url.searchParams)), definition);
});

test("saving captures the base view and every effective flat filter", () => {
  const definition = issueDefinitionFromQuery({ view: "critical", query: "authorization", severity: "high", status: "regressed", category: "Security", repository: "team/service", provider: "gitlab", api_base_url: "http://gitlab.internal/api/v4", path: "src/api", assignee: "me", rule: "security", age: "7d" });
  assert.equal(definition.view, "critical");
  assert.equal(definition.filters.items.length, 11);
  assert.ok(definition.filters.items.some((item) => item.field === "query" && item.operator === "contains"));
  assert.ok(definition.filters.items.some((item) => item.field === "age" && item.operator === "within"));
});

test("applying a view discards cursor, selection, old flat filters and time anchor", () => {
  const definition = issueDefinitionFromQuery({ view: "resolved", query: "race", cursor: "old", direction: "after", selected: "old-issue", filter_time: "2026-09-22T10:00:00Z", age: "24h", since: "2026-09-21T10:00:00Z" });
  const url = new URL(issueDefinitionHref("acme", definition), "http://console.test");
  assert.deepEqual([...url.searchParams.keys()], ["view", "filters"]);
  assert.equal(issueFilterAnchor({ filter_time: "2026-09-22T10:00:00Z", cursor: "next" }), "2026-09-22T10:00:00Z");
  assert.equal(issueFilterAnchor({ since: "2026-09-21T10:00:00Z", age: "24h" }), "2026-09-22T10:00:00.000Z");
});

test("empty AND is valid and clear preserves only base view and saved identity", () => {
  assert.deepEqual(parseIssueFilters(), emptyIssueFilters());
  assert.equal(issueDefinitionHref("acme", { view: "assigned", filters: emptyIssueFilters() }, "mine"), "/acme/issues?view=assigned&saved_view=mine");
});

test("dirty comparison normalizes predicate property order and whitespace", () => {
  assert.equal(issueDefinitionKey({ view: "open", filters: group([condition("query", " race ", "contains")]) }), issueDefinitionKey({ view: "open", filters: { items: [{ value: "race", operator: "contains", field: "query" }], condition: "and" } }));
});

test("invalid URL input never falls back to an unfiltered or empty result", () => {
  assert.throws(() => parseIssueFilters("{oops"), /invalid JSON/);
  assert.throws(() => issueDefinitionFromQuery({ view: "surprise" }), /not supported/);
  assert.throws(() => issueFilterAnchor({ filter_time: "yesterday" }), /invalid/);
  assert.throws(() => issueDefinitionFromQuery({ since: "2026-01-01" }), /legacy date/);
});

test("strict union validates operators, options, depth and empty groups", () => {
  for (const input of [group([], "or"), group([group([])]), group([group([group([condition()])])]), group([condition("severity", "high", "contains")]), group([condition("provider", "bitbucket")]), group([condition("unknown", "value")]), { condition: "and", items: [], field: null }, { condition: "and" }, group([null])]) {
    assert.throws(() => validateIssueFilters(input));
  }
});

test("limits enforce 20 leaves, 512 UTF8 bytes per value, NUL and 8KB payload", () => {
  assert.equal(validateIssueFilters(group(Array.from({ length: 20 }, () => condition()))).items.length, 20);
  assert.throws(() => validateIssueFilters(group(Array.from({ length: 21 }, () => condition()))), /20/);
  assert.throws(() => validateIssueFilters(group([condition("query", "中".repeat(171))])), /512/);
  assert.throws(() => validateIssueFilters(group([condition("query", "a\0b")])), /NUL/);
  assert.throws(() => parseIssueFilters(" ".repeat(8193)), /8 KB/);
  assert.throws(() => validateIssueFilters(group(Array.from({ length: 20 }, () => condition("query", "x".repeat(500))))), /8 KB/);
});

test("combines root OR and flat filters using AND, never replaces either intent", () => {
  const definition = issueDefinitionFromQuery({ filters: JSON.stringify(group([condition("severity", "critical"), condition("severity", "high")], "or")), repository: "team/service" });
  assert.equal(definition.filters.condition, "and");
  assert.equal(definition.filters.items[0].condition, "or");
  assert.equal(definition.filters.items[1].field, "repository");
});

test("compact title strips simple markdown without modifying retained body", () => {
  const body = "**SQL Injection Vulnerability.** The `email` comes from [input](https://test.invalid).";
  assert.equal(issueListPreview(body), "SQL Injection Vulnerability. The email comes from input.");
  assert.equal(issueDisplayTitle(body), "SQL Injection Vulnerability.");
  assert.ok(body.startsWith("**"));
});

test("display title bounds verbose findings while keeping meaningful words", () => {
  const body = `The \`/api/health\` route remains in the codebase and returns a valid response (${"important ".repeat(30)}). Follow-up evidence is retained separately.`;
  const title = issueDisplayTitle(body);
  assert.ok(title.length <= 141);
  assert.ok(title.startsWith("The /api/health route"));
  assert.ok(title.endsWith("…"));
  assert.equal(issueDisplayTitle("A short finding without punctuation"), "A short finding without punctuation");
});

test("display summary shows one explanation sentence and preserves code identifiers", () => {
  const body = "**Critical command injection vulnerability.** The `get_user_action` value reaches `/bin/sh -c` without validation. Mitigation options follow with a long code sample.";
  assert.equal(issueDisplaySummary(body), "The get_user_action value reaches /bin/sh -c without validation.");
  assert.equal(issueListPreview("`get_user_action` is unsafe."), "get_user_action is unsafe.");
  assert.equal(issueDisplaySummary("One concise finding."), "One concise finding.");
});
