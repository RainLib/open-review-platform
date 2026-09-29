import assert from "node:assert/strict";
import { test } from "node:test";

import { exactScopeOutsideSelection, hasWildcardScope, repositoryScopeEntryCount, scopeFromVisibleSelection, selectedRepositoryIDsForScope } from "./repository-scope-selection.ts";

const repositories = [
  { external_id: "1", name: "team/visible", archived: false },
  { external_id: "2", name: "team/another", archived: false },
  { external_id: "3", name: "team/archived", archived: true },
];

test("checkbox edits preserve configured exact paths outside the selectable page", () => {
  const scope = "team/hidden,team/visible,team/archived";
  assert.deepEqual(exactScopeOutsideSelection(scope, repositories), ["team/hidden", "team/archived"]);
  assert.equal(scopeFromVisibleSelection(scope, repositories, new Set()), "team/archived,team/hidden");
  assert.equal(scopeFromVisibleSelection(scope, repositories, new Set(["2"])), "team/another,team/archived,team/hidden");
});

test("wildcard scope is explicitly replaced by selected exact paths", () => {
  assert.equal(hasWildcardScope("*/*"), true);
  assert.equal(hasWildcardScope("team/*,other/repo"), true);
  assert.equal(hasWildcardScope("team/visible"), false);
  assert.equal(scopeFromVisibleSelection("team/*", repositories, new Set(["1"])), "team/visible");
  assert.equal(scopeFromVisibleSelection("*/*", repositories, new Set(["1", "3"])), "team/visible");
});

test("selected paths and preserved exact paths are deduplicated and sorted", () => {
  assert.equal(
    scopeFromVisibleSelection("team/hidden,team/hidden,team/visible", repositories, new Set(["1", "2"])),
    "team/another,team/hidden,team/visible",
  );
});

test("advanced scope edits update visible selection before checkbox changes", () => {
  const typedScope = "team/another,team/hidden";
  assert.deepEqual([...selectedRepositoryIDsForScope(typedScope, repositories)], ["2"]);
  assert.equal(
    scopeFromVisibleSelection(typedScope, repositories, new Set(["1", "2"])),
    "team/another,team/hidden,team/visible",
  );
  assert.deepEqual([...selectedRepositoryIDsForScope("team/*", repositories)], ["1", "2"]);
  assert.deepEqual([...selectedRepositoryIDsForScope("team/archived", repositories)], []);
});

test("an empty in-scope inventory still permits a draft exact scope correction", () => {
  const typedScope = "team/corrected";
  assert.deepEqual([...selectedRepositoryIDsForScope(typedScope, [])], []);
  assert.deepEqual(exactScopeOutsideSelection(typedScope, []), [typedScope]);
  assert.equal(scopeFromVisibleSelection(typedScope, [], new Set()), typedScope);
});

test("scope entry count follows the control-plane limit boundary", () => {
  assert.equal(repositoryScopeEntryCount(""), 0);
  assert.equal(repositoryScopeEntryCount("team/one, team/two"), 2);
  assert.equal(repositoryScopeEntryCount("team/*"), 1);
});
