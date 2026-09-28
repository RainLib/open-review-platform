"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, LoaderCircle, Save, Search, TriangleAlert } from "lucide-react";
import { useRouter } from "next/navigation";

import type { ProviderRepository } from "@/lib/control-api";
import { MAX_REPOSITORY_SCOPE_ENTRIES, exactScopeOutsideSelection, hasWildcardScope, repositoryScopeEntryCount, scopeFromVisibleSelection, selectedRepositoryIDsForScope } from "@/lib/repository-scope-selection";

export function InstallationRepositoryScopeEditor({
  active,
  currentScope,
  installationID,
  org,
  repositories,
  returnTo,
  onScopeDirtyChange,
}: {
  active: boolean;
  currentScope: string;
  installationID: string;
  org: string;
  repositories: ProviderRepository[];
  returnTo?: string;
  onScopeDirtyChange?: (dirty: boolean) => void;
}) {
  const router = useRouter();
  const [scope, setScope] = useState(currentScope);
  const [query, setQuery] = useState("");
  const [searchResult, setSearchResult] = useState<{
    query: string;
    repositories: ProviderRepository[];
    error?: string;
  }>();
  const [pending, setPending] = useState(false);
  const [pendingWildcardScope, setPendingWildcardScope] = useState<string>();
  const [message, setMessage] = useState<string>();
  const searchTerm = query.trim();
  useEffect(() => {
    if (searchTerm.length < 2) return;
    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}/repositories?query=${encodeURIComponent(searchTerm)}`,
          { cache: "no-store", signal: controller.signal },
        );
        const payload = (await response.json()) as { repositories?: ProviderRepository[]; error?: string };
        if (!response.ok) throw new Error(payload.error ?? "Repository search failed.");
        if (controller.signal.aborted) return;
        setSearchResult({ query: searchTerm, repositories: payload.repositories ?? [] });
      } catch (error) {
        if (controller.signal.aborted) return;
        setSearchResult({
          query: searchTerm,
          repositories: [],
          error: error instanceof Error ? error.message : "Repository search failed.",
        });
      }
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [installationID, org, searchTerm]);
  const currentSearch = searchResult?.query === searchTerm ? searchResult : undefined;
  const knownRepositories = useMemo(() => {
    const known = new Map(repositories.map((repository) => [repository.external_id, repository]));
    for (const repository of searchResult?.repositories ?? []) known.set(repository.external_id, repository);
    return [...known.values()];
  }, [repositories, searchResult]);
  const selected = selectedRepositoryIDsForScope(scope, knownRepositories);
  const visibleRepositories = searchTerm.length >= 2 ? currentSearch?.repositories ?? [] : repositories;
  const activeRepositories = visibleRepositories.filter((repository) => !repository.archived);
  const matchingRepositories = searchTerm.length === 1 ? [] : activeRepositories;
  const selectedRepositories = matchingRepositories.filter((repository) => selected.has(repository.external_id));
  const outsideSelection = exactScopeOutsideSelection(scope, knownRepositories);
  const hasBroadScope = hasWildcardScope(scope);
  const scopeEntryCount = repositoryScopeEntryCount(scope);
  useEffect(() => {
    onScopeDirtyChange?.(scope.trim() !== currentScope);
  }, [currentScope, onScopeDirtyChange, scope]);

  function applyScope(nextScope: string) {
    if (!nextScope) {
      setMessage("Choose at least one repository before replacing the review scope.");
      setPendingWildcardScope(undefined);
      return;
    }
    if (repositoryScopeEntryCount(nextScope) > MAX_REPOSITORY_SCOPE_ENTRIES) {
      setMessage(`A review scope can contain at most ${MAX_REPOSITORY_SCOPE_ENTRIES} exact repositories. Clear other selections or use an approved group/* scope.`);
      setPendingWildcardScope(undefined);
      return;
    }
    setScope(nextScope);
    setPendingWildcardScope(undefined);
    setMessage(undefined);
  }

  function replaceWithSelection(next: Set<string>) {
    const nextScope = scopeFromVisibleSelection(scope, knownRepositories, next);
    if (hasWildcardScope(scope)) {
      if (!nextScope || repositoryScopeEntryCount(nextScope) > MAX_REPOSITORY_SCOPE_ENTRIES) {
        applyScope(nextScope);
        return;
      }
      setPendingWildcardScope(nextScope);
      return;
    }
    applyScope(nextScope);
  }

  async function saveScope() {
    const nextScope = scope.trim();
    if (!active || !nextScope || scopeEntryCount > MAX_REPOSITORY_SCOPE_ENTRIES || nextScope === currentScope || pending) return;
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}`,
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            repository_scope: nextScope,
            expected_repository_scope: currentScope,
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) throw new Error(payload.error ?? "Repository scope could not be updated.");
      if (returnTo) router.replace(returnTo);
      else router.refresh();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Repository scope could not be updated.");
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-5">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-start">
        <div>
          <p className="text-sm font-semibold text-[var(--ls-text)]">Review scope</p>
          <p className="mt-1 max-w-2xl text-xs leading-5 text-[var(--ls-text-secondary)]">
            Select synchronized repositories within the current scope for a precise allowlist, or retain an approved self-managed <code className="font-mono">group/*</code> scope. Repositories outside this scope are not shown. Saving uses the displayed current scope as a compare-and-set boundary.
          </p>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-warning-text)]">
            Saving a new scope pauses this installation&apos;s review admission until a fresh read-only provider verification succeeds.
          </p>
        </div>
        <button
          className="luminous-focus inline-flex h-9 shrink-0 items-center justify-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-45"
          disabled={!active || !scope.trim() || scopeEntryCount > MAX_REPOSITORY_SCOPE_ENTRIES || scope.trim() === currentScope || pending}
          onClick={saveScope}
          type="button"
        >
          {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
          {pending ? "Saving…" : "Save scope"}
        </button>
      </div>
      {outsideSelection.length ? (
        <div className="mt-4 flex gap-2 rounded-[10px] border border-amber-500/25 bg-amber-500/[0.06] px-3 py-2.5 text-xs leading-5 text-[var(--ls-warning-text)]">
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
          {outsideSelection.length} exact repository scope {outsideSelection.length === 1 ? "entry is" : "entries are"} outside the selectable list. Checkbox changes preserve them; edit Advanced scope to remove them.
        </div>
      ) : null}
      {hasBroadScope ? (
        <div className="mt-4 flex gap-2 rounded-[10px] border border-amber-500/25 bg-amber-500/[0.06] px-3 py-2.5 text-xs leading-5 text-[var(--ls-warning-text)]">
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
          The current scope includes a wildcard. Checkbox changes require confirmation because replacing it with exact selections can exclude repositories that are not synchronized or displayed here.
        </div>
      ) : null}
      {pendingWildcardScope ? (
        <div className="mt-3 rounded-[10px] border border-amber-500/35 bg-amber-500/[0.08] p-3 text-xs leading-5 text-[var(--ls-warning-text)]" role="alert">
          <p className="font-semibold">Replace wildcard review scope?</p>
          <p className="mt-1">This will keep only {repositoryScopeEntryCount(pendingWildcardScope)} exact repository entries. Repositories not shown here will be excluded from review.</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <button className="luminous-focus rounded-[8px] border border-amber-500/40 px-2.5 py-1.5 font-semibold" onClick={() => applyScope(pendingWildcardScope)} type="button">Replace wildcard</button>
            <button className="luminous-focus rounded-[8px] px-2.5 py-1.5" onClick={() => setPendingWildcardScope(undefined)} type="button">Keep current scope</button>
          </div>
        </div>
      ) : null}
      {repositories.length >= 500 ? (
        <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">Showing the first 500 synchronized repositories in this scope. Search the server by repository name to find synchronized repositories beyond this page; other exact entries remain configured.</p>
      ) : null}
      <p className="mt-3 text-xs leading-5 text-[var(--ls-text-tertiary)]">Search covers the worker-synchronized inventory within this scope. If the provider reports a partial inventory, enter an exact repository path in Advanced scope and reverify the connection.</p>
      <label className="mt-4 flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-[var(--ls-text-secondary)]">
        <Search aria-hidden="true" className="size-4 shrink-0" />
        <span className="sr-only">Search synchronized repositories</span>
        <input
          className="h-full min-w-0 flex-1 bg-transparent text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]"
          maxLength={120}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search synchronized repositories by name"
          type="search"
          value={query}
        />
      </label>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          className="luminous-focus rounded-[8px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2.5 py-1.5 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-white/70 disabled:opacity-45 dark:hover:bg-white/5"
          disabled={!active || pending || !matchingRepositories.length}
          onClick={() => replaceWithSelection(new Set([...selected, ...matchingRepositories.map((repository) => repository.external_id)]))}
          type="button"
        >
          Select all matches
        </button>
        <button
          className="luminous-focus rounded-[8px] px-2.5 py-1.5 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface)] disabled:opacity-45"
          disabled={!active || pending || !matchingRepositories.length}
          onClick={() => replaceWithSelection(new Set([...selected].filter((id) => !matchingRepositories.some((repository) => repository.external_id === id))))}
          type="button"
        >
          Clear matches
        </button>
        <span className="text-xs text-[var(--ls-text-tertiary)]">{matchingRepositories.length} shown · {selectedRepositories.length} selected{outsideSelection.length ? ` · ${outsideSelection.length} exact entries preserved` : ""}</span>
      </div>
      {searchTerm.length === 1 ? <p className="mt-3 text-xs text-[var(--ls-text-tertiary)]">Enter at least 2 characters to search the synchronized inventory.</p> : null}
      {searchTerm.length >= 2 && !currentSearch ? <p className="mt-3 text-xs text-[var(--ls-text-tertiary)]">Searching synchronized repositories…</p> : null}
      {currentSearch?.error ? <p className="mt-3 text-xs text-[var(--ls-danger-text)]">{currentSearch.error}</p> : null}
      {currentSearch && currentSearch.repositories.length >= 500 ? <p className="mt-3 text-xs text-[var(--ls-warning-text)]">Search reached the 500-result limit. Use a more specific repository name.</p> : null}
      {matchingRepositories.length ? (
        <div className="mt-3 grid max-h-[360px] gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
          {matchingRepositories.map((repository) => {
            const checked = selected.has(repository.external_id);
            return (
              <label className="flex cursor-pointer items-center gap-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 py-2.5 text-sm text-[var(--ls-text)] transition hover:border-[var(--ls-line-strong)]" key={repository.external_id}>
                <input
                  checked={checked}
                  className="size-4 accent-[var(--ls-accent)]"
                  disabled={!active || pending}
                  onChange={() => {
                    const next = new Set(selected);
                    if (checked) next.delete(repository.external_id);
                    else next.add(repository.external_id);
                    replaceWithSelection(next);
                  }}
                  type="checkbox"
                />
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{repository.name}</span>
                {checked ? <Check className="size-3.5 shrink-0 text-[var(--ls-accent)]" /> : null}
              </label>
            );
          })}
        </div>
      ) : !searchTerm ? (
        <p className="mt-3 text-xs leading-5 text-[var(--ls-text-tertiary)]">
          No synchronized repositories match the current scope. You can enter an exact repository or an approved group/* scope in Advanced scope below; setup will wait for a matching worker-synchronized repository before continuing.
        </p>
      ) : searchTerm.length >= 2 && currentSearch && !currentSearch.error ? (
        <p className="mt-3 text-xs leading-5 text-[var(--ls-text-tertiary)]">No synchronized repositories match this search or the current review scope.</p>
      ) : null}
      <label className="mt-4 block">
        <span className="mb-1.5 block text-xs font-medium text-[var(--ls-text-secondary)]">Advanced scope</span>
        <input
          className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 font-mono text-xs text-[var(--ls-text)]"
          disabled={!active}
          onChange={(event) => {
            setScope(event.target.value);
            setPendingWildcardScope(undefined);
            setMessage(undefined);
          }}
          value={scope}
        />
        <span className="mt-1.5 block text-[11px] leading-4 text-[var(--ls-text-tertiary)]">{scopeEntryCount} / {MAX_REPOSITORY_SCOPE_ENTRIES} entries. Use comma-separated <code className="font-mono">owner/repository</code> entries or an approved <code className="font-mono">group/*</code> prefix. Final validation occurs in the control plane.</span>
      </label>
      {message ? <p className="mt-3 text-xs leading-5 text-[var(--ls-danger-text)]">{message}</p> : null}
    </section>
  );
}
