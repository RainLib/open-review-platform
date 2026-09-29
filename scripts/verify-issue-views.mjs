// Exercise the live Console BFF, control API and database in the explicit local
// preview profile. No provider writes or model calls are made. Only the view
// created by this invocation is removed; existing views/issues are untouched.
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

const origin = new URL(process.env.OPEN_REVIEW_ACCEPTANCE_ORIGIN ?? "http://127.0.0.1:3110");
const tenant = process.env.OPEN_REVIEW_ACCEPTANCE_TENANT;
if (!["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) || origin.protocol !== "http:" || origin.username || origin.password) {
  throw new Error("This acceptance script is restricted to a loopback HTTP local-preview Console.");
}
if (!tenant || !/^[a-z0-9][a-z0-9-]{1,62}$/.test(tenant)) {
  throw new Error("Set OPEN_REVIEW_ACCEPTANCE_TENANT to an explicit disposable acceptance workspace slug.");
}

const login = await fetch(new URL("/api/auth/local?next=%2Fworkspaces", origin), {
  redirect: "manual", signal: AbortSignal.timeout(20_000),
});
assert.equal(login.status, 307, "Explicit local preview login must be enabled");
const cookie = login.headers.getSetCookie().map((entry) => entry.split(";")[0]).join("; ");
assert.ok(cookie, "Local preview session is required");
const base = `/api/tenants/${encodeURIComponent(tenant)}`;
const emit = (check, extra = {}) => console.log(JSON.stringify({ check, ...extra }));
async function request(path, { method = "GET", body, status = 200 } = {}) {
  const response = await fetch(new URL(base + path, origin), {
    method,
    redirect: "manual",
    headers: { Cookie: cookie, ...(body ? { "Content-Type": "application/json" } : {}) },
    body: body ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(20_000),
  });
  assert.equal(response.status, status, `${method} ${path.split("?")[0]} returned an unexpected status`);
  if (status === 204) return undefined;
  return response.json();
}
const atom = (field, operator, value) => ({ field, operator, value });
const expression = {
  condition: "and",
  items: [
    { condition: "or", items: [atom("provider", "is", "github"), atom("provider", "is", "gitlab")] },
    atom("age", "within", "30d"),
  ],
};
const definition = { view: "all", filters: expression };
const listBefore = await request("/issue-views");
assert.ok(Array.isArray(listBefore.views), "Saved views must come from the live API");
let created;
try {
  created = await request("/issue-views", {
    method: "POST", status: 201,
    body: { name: `Acceptance ${randomUUID().slice(0, 8)}`, visibility: "personal", definition },
  });
  assert.ok(created.id && created.can_manage && created.revision === 1, "Created personal view receipt is incomplete");
  assert.deepEqual(created.definition, definition, "Nested filter definition must round-trip");
  emit("create-personal-view", { id: created.id, revision: created.revision });

  const listed = (await request("/issue-views")).views.find((view) => view.id === created.id);
  assert.deepEqual(listed.definition, definition, "A second read must restore the persisted definition");
  const first = await request(`/issues?${new URLSearchParams({ limit: "1", filters: JSON.stringify(expression) })}`);
  assert.ok(Array.isArray(first.issues));
  assert.ok(Number.isInteger(first.total_count) && first.total_count >= first.issues.length);
  assert.ok(Number.isFinite(Date.parse(first.filter_time)), "Relative age requires a stable pagination anchor");
  if (first.next_cursor) {
    const next = await request(`/issues?${new URLSearchParams({
      limit: "1", filters: JSON.stringify(expression), filter_time: first.filter_time, cursor: first.next_cursor,
    })}`);
    assert.equal(next.filter_time, first.filter_time);
    assert.notEqual(next.issues[0]?.id, first.issues[0]?.id, "Keyset pagination must advance");
    await request(`/issues?${new URLSearchParams({
      limit: "1", filters: JSON.stringify({ condition: "and", items: [atom("severity", "is", "critical")] }),
      filter_time: first.filter_time, cursor: first.next_cursor,
    })}`, { status: 400 });
    emit("pagination-anchor-and-cursor-scope");
  } else {
    emit("pagination-runtime-not-exercised", { reason: "Live result fits one page; pagination is covered by isolated database regressions." });
  }
  emit("live-grouped-filter-query", { totalCount: first.total_count, loaded: first.issues.length });

  const revision = created.revision;
  created = await request(`/issue-views/${created.id}`, {
    method: "PUT", body: { name: `${created.name} renamed`, visibility: "personal", definition: { view: "all", filters: { condition: "and", items: [] } }, revision },
  });
  assert.equal(created.revision, revision + 1);
  assert.deepEqual(created.definition.filters, { condition: "and", items: [] }, "An unfiltered saved view must retain the explicit empty AND contract");
  await request(`/issue-views/${created.id}`, {
    method: "PUT", status: 409,
    body: { name: "Stale update must fail", visibility: "personal", definition, revision },
  });
  const retained = (await request("/issue-views")).views.find((view) => view.id === created.id);
  assert.equal(retained.name, created.name, "Stale writes must not overwrite the saved view");
  emit("rename-and-revision-conflict");
  await request(`/issues?${new URLSearchParams({ filters: '{"condition":"or","items":[{"field":"tenant_id","operator":"is","value":"other"}]}' })}`, { status: 400 });
  await request(`/issues?${new URLSearchParams({ filters: '{"condition":"or","items":[]}' })}`, { status: 400 });
  await request(`/issues?${new URLSearchParams({ filters: '{"condition":"and","items":null}' })}`, { status: 400 });
  await request(`/issues?${new URLSearchParams({ filters: '{"condition":"and","items":[],"field":""}' })}`, { status: 400 });
  emit("invalid-filters-rejected");
} finally {
  if (created?.id) {
    await request(`/issue-views/${created.id}?revision=${created.revision}`, { method: "DELETE", status: 204 });
    const remaining = (await request("/issue-views")).views;
    assert.ok(!remaining.some((view) => view.id === created.id), "Acceptance view should be removed");
    assert.ok(listBefore.views.every((view) => remaining.some((item) => item.id === view.id)), "Existing views must remain untouched");
    emit("remove-own-test-view-and-preserve-existing-views");
  }
}
