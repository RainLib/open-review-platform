import type { ReviewEvidence, ReviewFindingEvidence } from "./control-api";

export type ReviewFileRow = {
  path: string;
  findings: ReviewFindingEvidence[];
  scope: "selected" | "deferred" | "finding-only";
  score?: number;
  reasons: string[];
  changeType?: string;
  previousPath?: string;
  additions?: number;
  deletions?: number;
  statsKnown?: boolean;
  binary?: boolean;
};

export type ReviewFileView = "files" | "dependencies" | "coverage" | "blast-radius";

export function validReviewFileView(value?: string): ReviewFileView {
  return value === "dependencies" || value === "coverage" || value === "blast-radius"
    ? value
    : "files";
}

export function reviewFileRows(evidence: ReviewEvidence): ReviewFileRow[] {
  const findingsByPath = new Map<string, ReviewFindingEvidence[]>();
  for (const finding of evidence.findings) {
    findingsByPath.set(finding.path, [...(findingsByPath.get(finding.path) ?? []), finding]);
  }

  const seen = new Set<string>();
  const rows: ReviewFileRow[] = [];
  for (const file of evidence.execution_plan?.file_scopes ?? []) {
    if (!file.path || seen.has(file.path)) continue;
    seen.add(file.path);
    rows.push({
      path: file.path,
      findings: findingsByPath.get(file.path) ?? [],
      scope: file.selected ? "selected" : "deferred",
      score: file.score,
      reasons: file.reasons ?? [],
      changeType: file.change_type,
      previousPath: file.previous_path,
      additions: file.additions,
      deletions: file.deletions,
      statsKnown: file.stats_known,
      binary: file.binary,
    });
  }
  for (const path of evidence.execution_plan?.selected_paths ?? []) {
    if (!path || seen.has(path)) continue;
    seen.add(path);
    rows.push({ path, findings: findingsByPath.get(path) ?? [], scope: "selected", reasons: [] });
  }
  for (const [path, findings] of findingsByPath) {
    if (seen.has(path)) continue;
    rows.push({ path, findings, scope: "finding-only", reasons: [] });
  }
  return rows;
}

export function reviewFileSummary(evidence: ReviewEvidence, rows: ReviewFileRow[]) {
  const selected = rows.filter((file) => file.scope === "selected").length;
  const deferredRows = rows.filter((file) => file.scope === "deferred").length;
  const findingOnly = rows.filter((file) => file.scope === "finding-only").length;
  const deferred = Math.max(evidence.execution_plan?.deferred_files ?? 0, deferredRows);
  return { selected, deferred, findingOnly, unknownDeferred: deferred - deferredRows };
}

const DEPENDENCY_MANIFESTS = new Set([
  "package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
  "go.mod", "go.sum", "cargo.toml", "cargo.lock", "pom.xml", "build.gradle",
  "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.lockfile",
  "pyproject.toml", "poetry.lock", "uv.lock", "pipfile", "pipfile.lock",
  "composer.json", "composer.lock", "gemfile", "gemfile.lock", "mix.exs", "mix.lock",
  "pubspec.yaml", "pubspec.lock", "package.swift", "podfile", "podfile.lock",
  "directory.packages.props", "nuget.config",
]);

export function isDependencyManifestPath(path: string) {
  const filename = path.split("/").at(-1)?.toLowerCase() ?? "";
  return DEPENDENCY_MANIFESTS.has(filename)
    || /^requirements(?:[-_.][^/]*)?\.txt$/.test(filename)
    || /\.(?:csproj|fsproj|vbproj)$/.test(filename);
}

export function dependencyManifestRows(rows: ReviewFileRow[]) {
  return rows.filter((row) => isDependencyManifestPath(row.path));
}

export function prioritizedReviewFileRows(rows: ReviewFileRow[]) {
  return rows
    .filter((row) => row.scope === "selected")
    .sort((left, right) => (right.score ?? -1) - (left.score ?? -1) || left.path.localeCompare(right.path));
}

export function filterReviewFileRows(
  rows: ReviewFileRow[],
  query: string,
  scope: string,
  change: string,
) {
  const term = query.trim().toLocaleLowerCase();
  return rows.filter((row) =>
    (!term || row.path.toLocaleLowerCase().includes(term))
    && (scope === "all" || row.scope === scope)
    && (change === "all" || row.changeType === change),
  );
}
