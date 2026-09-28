import assert from "node:assert/strict";
import { test } from "node:test";

import {
  dependencyManifestRows,
  filterReviewFileRows,
  isDependencyManifestPath,
  prioritizedReviewFileRows,
  reviewFileRows,
  reviewFileSummary,
  validReviewFileView,
} from "./review-file-analysis.ts";

const finding = (path, severity = "high") => ({ path, severity, id: `${path}:${severity}` });

test("file evidence keeps the immutable admission scope and never upgrades a finding-only path", () => {
  const evidence = {
    execution_plan: {
      selected_paths: ["src/auth.ts", "src/auth.ts", "go.mod"],
      deferred_files: 3,
      file_scopes: [
        { path: "src/auth.ts", selected: true, score: 95, reasons: ["authentication boundary"], change_type: "modified" },
        { path: "docs/notes.md", selected: false, score: 4, reasons: ["documentation"] },
      ],
    },
    findings: [finding("src/auth.ts"), finding("src/missing.ts", "medium")],
  };
  const rows = reviewFileRows(evidence);
  assert.deepEqual(rows.map((row) => [row.path, row.scope]), [
    ["src/auth.ts", "selected"],
    ["docs/notes.md", "deferred"],
    ["go.mod", "selected"],
    ["src/missing.ts", "finding-only"],
  ]);
  assert.deepEqual(reviewFileSummary(evidence, rows), {
    selected: 2,
    deferred: 3,
    findingOnly: 1,
    unknownDeferred: 2,
  });
  assert.deepEqual(prioritizedReviewFileRows(rows).map((row) => row.path), ["src/auth.ts", "go.mod"]);
  assert.equal(rows[0].findings.length, 1);
});

test("manifest hints come only from retained path names, not inferred dependency edges", () => {
  const paths = ["apps/web/package.json", "pkg/go.mod", "svc/requirements-dev.txt", "src/package.json.ts", "docs/dependencies.md"];
  assert.deepEqual(paths.map(isDependencyManifestPath), [true, true, true, false, false]);
  assert.deepEqual(
    dependencyManifestRows(paths.map((path) => ({ path }))).map((row) => row.path),
    paths.slice(0, 3),
  );
});

test("file filters are exact for scope/change and case-insensitive for paths", () => {
  const rows = [
    { path: "src/Auth.ts", scope: "selected", changeType: "modified" },
    { path: "src/auth.test.ts", scope: "deferred", changeType: "added" },
  ];
  assert.deepEqual(filterReviewFileRows(rows, "AUTH", "selected", "modified").map((row) => row.path), ["src/Auth.ts"]);
  assert.deepEqual(filterReviewFileRows(rows, "auth", "deferred", "modified"), []);
  assert.equal(validReviewFileView("blast-radius"), "blast-radius");
  assert.equal(validReviewFileView("unknown"), "files");
});
