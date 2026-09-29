"use client";

import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Bookmark, Check, Filter, Pencil, Plus, Trash2, Users } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { IssueFilterBuilder, issueFilterButtonClass, issueFilterControlClass } from "@/components/console/issue-filter-builder";
import { countIssueFilters, emptyIssueFilters, issueDefinitionHref, issueDefinitionKey, issueViews, validateIssueFilters, type IssueSavedView, type IssueViewDefinition } from "@/lib/issue-filters";
import type { IssueViewsData } from "@/lib/control-api";

const primary = "luminous-focus h-10 rounded-[10px] bg-[var(--ls-accent)] px-4 text-white hover:bg-[var(--ls-accent-hover)]";

export function IssueViewControls({ org, definition, invalidFilter, savedID, data }: {
  org: string; definition?: IssueViewDefinition; invalidFilter?: string; savedID?: string; data: IssueViewsData;
}) {
  const router = useRouter();
  const [views, setViews] = useState(data.views);
  const [live, setLive] = useState(data.source === "live");
  const [canCreateWorkspace, setCanCreateWorkspace] = useState(data.canCreateWorkspace);
  const [filterOpen, setFilterOpen] = useState(false);
  const [draft, setDraft] = useState<IssueViewDefinition>(definition ?? { view: "open", filters: emptyIssueFilters() });
  const [saveMode, setSaveMode] = useState<"create" | "rename" | null>(null);
  const [name, setName] = useState("");
  const [visibility, setVisibility] = useState<"personal" | "workspace">("personal");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const saveTrigger = useRef<HTMLButtonElement | null>(null);
  const viewSelect = useRef<HTMLSelectElement | null>(null);
  const selected = views.find((view) => view.id === savedID);
  const dirty = Boolean(selected && definition && issueDefinitionKey(selected.definition) !== issueDefinitionKey(definition));
  let draftError = "";
  try { validateIssueFilters(draft.filters); } catch (error) { draftError = error instanceof Error ? error.message : "Invalid filters."; }

  function apply(next: IssueViewDefinition, id = savedID) {
    router.push(issueDefinitionHref(org, next, id));
    setFilterOpen(false);
  }

  async function mutate(method: "POST" | "PUT" | "DELETE", view?: IssueSavedView, body?: unknown) {
    setBusy(true); setError(""); setConflict(false);
    try {
      const suffix = view ? `/${encodeURIComponent(view.id)}${method === "DELETE" ? `?revision=${view.revision}` : ""}` : "";
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/issue-views${suffix}`, {
        method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined,
      });
      if (!response.ok) {
        if (response.status === 409) {
          setConflict(true);
          throw new Error("This view changed, its name is already used, or the view limit was reached. Your draft is retained. Reload saved views before retrying, or save a personal copy with a different name.");
        }
        if (response.status === 403) throw new Error("You cannot change this view. Workspace views require an owner or administrator; save a personal copy instead.");
        if (response.status === 404) throw new Error("This saved view was deleted or is no longer accessible. Reload saved views or save a personal copy.");
        throw new Error("The view could not be saved. Check your connection and retry; your draft is retained.");
      }
      if (method === "DELETE") {
        setViews((current) => current.filter((item) => item.id !== view?.id));
        setDeleteOpen(false);
        if (definition) router.push(issueDefinitionHref(org, definition));
      } else {
        const saved = await response.json() as IssueSavedView;
        // Reject malformed success responses instead of pretending a save succeeded.
        if (!saved.id || !Number.isInteger(saved.revision) || !saved.definition) throw new Error("The server returned an incomplete view. Reload saved views to check whether it was saved.");
        validateIssueFilters(saved.definition.filters);
        setViews((current) => [...current.filter((item) => item.id !== saved.id), saved]);
        setSaveMode(null);
        // Renaming must not discard the unsaved current filter definition.
        router.push(issueDefinitionHref(org, method === "PUT" && saveMode === "rename" && definition ? definition : saved.definition, saved.id));
      }
      router.refresh();
    } catch (error) { setError(error instanceof Error ? error.message : "Saved views are unavailable."); }
    finally { setBusy(false); }
  }

  async function reloadViews() {
    setBusy(true);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/issue-views`, { cache: "no-store" });
      if (!response.ok) throw new Error("Saved views could not be reloaded. Your filters are unchanged.");
      const result = await response.json() as { views: IssueSavedView[]; can_create_workspace: boolean };
      if (!Array.isArray(result.views)) throw new Error("Saved views returned an invalid response.");
      result.views.forEach((view) => validateIssueFilters(view.definition.filters));
      setViews(result.views); setCanCreateWorkspace(result.can_create_workspace); setLive(true); setError(""); setConflict(false);
    } catch (error) { setError(error instanceof Error ? error.message : "Saved views are unavailable."); }
    finally { setBusy(false); }
  }

  function openSave(mode: "create" | "rename", trigger: HTMLButtonElement) {
    saveTrigger.current = trigger;
    setName(mode === "rename" && selected ? selected.name : "");
    setVisibility(mode === "rename" && selected ? selected.visibility : "personal");
    setError(""); setConflict(false); setSaveMode(mode);
  }

  const feedback = error ? <div className="rounded-[12px] border border-amber-500/25 bg-amber-500/5 p-3 text-sm text-[var(--ls-text)]" role="alert"><p>{error}</p><Button className={`${issueFilterButtonClass} mt-2`} disabled={busy} onClick={reloadViews} type="button">Reload saved views</Button></div> : null;

  return <section aria-label="Saved views and grouped filters" className="space-y-3">
    <div className="flex flex-wrap items-center gap-2">
      <Bookmark aria-hidden="true" className="size-4 text-[var(--ls-text-tertiary)]" />
      <label className="min-w-0 max-w-full flex-1 sm:max-w-64"><span className="sr-only">Saved views</span><select className={issueFilterControlClass} disabled={!live || busy} ref={viewSelect} onChange={(event) => { const chosen = views.find((view) => view.id === event.target.value); if (chosen) apply(chosen.definition, chosen.id); else if (definition) router.push(issueDefinitionHref(org, definition)); }} value={selected?.id ?? ""}>
        <option value="">{savedID && !selected ? "View unavailable · current filters" : "Unsaved view"}</option>
        <optgroup label="Personal · only you">{views.filter((view) => view.visibility === "personal").map((view) => <option key={view.id} value={view.id}>{view.name}</option>)}</optgroup>
        <optgroup label="Workspace · shared">{views.filter((view) => view.visibility === "workspace").map((view) => <option key={view.id} value={view.id}>{view.name}{view.can_manage ? "" : " (read-only)"}</option>)}</optgroup>
      </select></label>
      {selected ? <span className="flex items-center gap-1 text-xs text-[var(--ls-text-secondary)]">{selected.visibility === "workspace" ? <Users className="size-3.5" /> : <Bookmark className="size-3.5" />}{selected.visibility === "workspace" ? "Shared" : "Personal"}{dirty ? " · Unsaved changes" : ""}</span> : null}
      <Dialog onOpenChange={setFilterOpen} open={filterOpen}>
        <DialogTrigger asChild><Button className={issueFilterButtonClass} disabled={!live} onClick={() => setDraft(definition ?? { view: "open", filters: emptyIssueFilters() })} type="button"><Filter className="size-4" />Filters{definition && countIssueFilters(definition.filters) ? ` (${countIssueFilters(definition.filters)})` : ""}</Button></DialogTrigger>
        <DialogContent>
          <DialogTitle className="pr-8 text-xl font-semibold tracking-tight">Filter issues</DialogTitle>
          <DialogDescription className="mt-2 text-sm text-[var(--ls-text-secondary)]">Combine conditions with AND / OR. The base view is always applied as well. Applying starts a new page and time window.</DialogDescription>
          {invalidFilter ? <p className="mt-3 text-sm text-amber-600 dark:text-amber-400" role="alert">The current link is invalid. Build new filters below to replace it.</p> : null}
          <label className="mb-4 mt-5 block space-y-2 text-sm font-medium">Base view<select className={issueFilterControlClass} value={draft.view} onChange={(event) => setDraft({ ...draft, view: event.target.value as IssueViewDefinition["view"] })}>{issueViews.map((view) => <option key={view} value={view}>{view === "assigned" ? "Assigned to me" : view[0].toUpperCase() + view.slice(1)}</option>)}</select></label>
          <IssueFilterBuilder onChange={(filters) => setDraft({ ...draft, filters })} value={draft.filters} />
          {draftError ? <p className="mt-3 text-sm text-amber-600 dark:text-amber-400" role="alert">{draftError}</p> : null}
          <div className="mt-6 flex flex-wrap justify-between gap-2"><Button className={issueFilterButtonClass} onClick={() => setDraft({ ...draft, filters: emptyIssueFilters() })} type="button">Clear conditions</Button><Button className={primary} disabled={Boolean(draftError)} onClick={() => apply(draft)} type="button"><Check className="size-4" />Apply filters</Button></div>
        </DialogContent>
      </Dialog>
      <Button className={issueFilterButtonClass} disabled={!live || !definition || busy} onClick={(event) => openSave("create", event.currentTarget)} type="button"><Plus className="size-4" />Save as new</Button>
      {selected?.can_manage ? <>
        <Button className={issueFilterButtonClass} disabled={!dirty || busy || !definition} onClick={() => mutate("PUT", selected, { name: selected.name, visibility: selected.visibility, revision: selected.revision, definition })} type="button">Update view</Button>
        <Button aria-label="Rename saved view" className={issueFilterButtonClass} disabled={busy} onClick={(event) => openSave("rename", event.currentTarget)} type="button"><Pencil className="size-4" /></Button>
        <AlertDialog onOpenChange={setDeleteOpen} open={deleteOpen}>
          <AlertDialogTrigger asChild><Button aria-label="Delete saved view" className={issueFilterButtonClass} disabled={busy} type="button"><Trash2 className="size-4" /></Button></AlertDialogTrigger>
          <AlertDialogContent className="border-[var(--ls-line)] bg-[var(--ls-surface)] text-[var(--ls-text)]" onCloseAutoFocus={(event) => { event.preventDefault(); viewSelect.current?.focus(); }}><AlertDialogTitle>Delete “{selected.name}”?</AlertDialogTitle><AlertDialogDescription>This removes the {selected.visibility === "workspace" ? "shared view for everyone in the workspace" : "view from your saved views"}. Issues and the current filters are not deleted.</AlertDialogDescription>{feedback}<AlertDialogFooter><AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel><AlertDialogAction disabled={busy} onClick={(event) => { event.preventDefault(); void mutate("DELETE", selected); }}>Delete view</AlertDialogAction></AlertDialogFooter></AlertDialogContent>
        </AlertDialog>
      </> : selected ? <span className="text-xs text-[var(--ls-text-tertiary)]">Read-only · save a personal copy to customize</span> : null}
      {definition && countIssueFilters(definition.filters) ? <Button className={issueFilterButtonClass} onClick={() => apply({ ...definition, filters: emptyIssueFilters() })} type="button">Clear filters</Button> : null}
    </div>
    {!live ? <div className="flex flex-wrap items-center gap-2"><p className="text-xs text-[var(--ls-text-secondary)]" role="status">{data.detail ?? "Saved views and grouped filters require a live workspace."}</p>{data.source !== "demo" ? <Button className={issueFilterButtonClass} disabled={busy} onClick={reloadViews} type="button">Retry saved views</Button> : null}</div> : null}
    {savedID && !selected && live ? <p className="text-xs text-amber-600 dark:text-amber-400" role="status">The saved view is unavailable or private. The explicit filters in this URL remain active; you can save a new personal view.</p> : null}
    {invalidFilter ? <p className="rounded-[12px] border border-amber-500/25 p-3 text-sm text-[var(--ls-text)]" role="alert">{invalidFilter} <a className="underline" href={`/${encodeURIComponent(org)}/issues?view=open`}>Clear invalid filters</a></p> : null}
    {!saveMode && !deleteOpen ? feedback : null}
    <Dialog onOpenChange={(open) => { if (!open && !busy) setSaveMode(null); }} open={saveMode !== null}>
      <DialogContent onCloseAutoFocus={(event) => { event.preventDefault(); saveTrigger.current?.focus(); }}>
        <DialogTitle className="pr-8 text-xl font-semibold tracking-tight">{saveMode === "rename" ? "Rename saved view" : "Save current view"}</DialogTitle>
        <DialogDescription className="mt-2 text-sm text-[var(--ls-text-secondary)]">{saveMode === "rename" ? "Only the name changes. Unsaved filters will not overwrite this view." : "Save the base view and every current filter, including search and last-seen period. Relative periods refresh each time you apply the view."}</DialogDescription>
        <form className="mt-5 space-y-4" onSubmit={(event) => { event.preventDefault(); if (saveMode === "rename" && selected) void mutate("PUT", selected, { name: name.trim(), visibility: selected.visibility, definition: selected.definition, revision: selected.revision }); else if (definition) void mutate("POST", undefined, { name: name.trim(), visibility, definition }); }}>
          <label className="block space-y-2 text-sm font-medium">View name<input autoComplete="off" className={issueFilterControlClass} maxLength={80} onChange={(event) => setName(event.target.value)} required value={name} /></label>
          <label className="block space-y-2 text-sm font-medium">Visibility<select className={issueFilterControlClass} disabled={saveMode === "rename"} onChange={(event) => setVisibility(event.target.value as "personal" | "workspace")} value={visibility}><option value="personal">Personal · only you</option><option disabled={!canCreateWorkspace} value="workspace">Workspace · shared with all members</option></select></label>
          {!canCreateWorkspace ? <p className="text-xs text-[var(--ls-text-secondary)]">Only owners and administrators can create or edit shared workspace views.</p> : null}
          {feedback}
          <div className="flex justify-end gap-2"><Button className={issueFilterButtonClass} disabled={busy} onClick={() => setSaveMode(null)} type="button">Cancel</Button><Button className={primary} disabled={busy || !name.trim() || conflict} type="submit">{busy ? "Saving…" : saveMode === "rename" ? "Rename view" : "Save view"}</Button></div>
        </form>
      </DialogContent>
    </Dialog>
  </section>;
}
