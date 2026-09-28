export type SelectableRepository = {
  external_id: string;
  name: string;
  archived: boolean;
};

export const MAX_REPOSITORY_SCOPE_ENTRIES = 100;

function scopeEntries(scope: string): string[] {
  return scope.split(",").map((entry) => entry.trim()).filter(Boolean);
}

function scopeAllowsRepository(scope: string, repository: string): boolean {
  return scopeEntries(scope).some((entry) => {
    const candidate = entry.replace(/^\/+|\/+$/g, "");
    return candidate === "*/*" || candidate === repository ||
      (candidate.endsWith("/*") && repository.startsWith(candidate.slice(0, -1)));
  });
}

export function selectedRepositoryIDsForScope(
  scope: string,
  repositories: SelectableRepository[],
): Set<string> {
  return new Set(repositories
    .filter((repository) => !repository.archived && scopeAllowsRepository(scope, repository.name))
    .map((repository) => repository.external_id));
}

export function hasWildcardScope(scope: string): boolean {
  return scopeEntries(scope).some((entry) => entry === "*/*" || entry.endsWith("/*"));
}

export function exactScopeOutsideSelection(scope: string, repositories: SelectableRepository[]): string[] {
  const selectableNames = new Set(repositories.filter((repository) => !repository.archived).map((repository) => repository.name));
  return [...new Set(scopeEntries(scope).filter((entry) =>
    entry !== "*/*" && !entry.endsWith("/*") && !selectableNames.has(entry),
  ))];
}

export function scopeFromVisibleSelection(
  currentScope: string,
  repositories: SelectableRepository[],
  selectedIDs: ReadonlySet<string>,
): string {
  const selectedNames = repositories
    .filter((repository) => !repository.archived && selectedIDs.has(repository.external_id))
    .map((repository) => repository.name);
  return [...new Set([...exactScopeOutsideSelection(currentScope, repositories), ...selectedNames])]
    .sort((left, right) => left.localeCompare(right))
    .join(",");
}

export function repositoryScopeEntryCount(scope: string): number {
  return scopeEntries(scope).length;
}
