"use client";

import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { countIssueFilters, isIssueFilterGroup, issueFilterFields, issueFilterOperatorLabels, type IssueFilterGroup, type IssueFilterPredicate } from "@/lib/issue-filters";

export const issueFilterControlClass = "luminous-focus h-10 min-w-0 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] disabled:opacity-50";
export const issueFilterButtonClass = "luminous-focus h-9 rounded-[10px] border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]";

const defaultPredicate = (): IssueFilterPredicate => ({ field: "severity", operator: "is", value: "high" });

export function IssueFilterBuilder({ value, onChange }: { value: IssueFilterGroup; onChange: (value: IssueFilterGroup) => void }) {
  return <GroupEditor count={countIssueFilters(value)} depth={1} label="Filters" onChange={onChange} value={value} />;
}

function GroupEditor({ value, onChange, depth, count, label }: { value: IssueFilterGroup; onChange: (value: IssueFilterGroup) => void; depth: number; count: number; label: string }) {
  function replace(index: number, item?: IssueFilterGroup | IssueFilterPredicate) {
    onChange({ ...value, items: item ? value.items.map((old, position) => position === index ? item : old) : value.items.filter((_, position) => position !== index) });
  }
  return <fieldset className={depth > 1 ? "min-w-0 rounded-[14px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] p-3" : "min-w-0"}>
    <legend className="sr-only">{label}</legend>
    <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-[var(--ls-text-secondary)]">Match
      <select aria-label={`${label} condition`} className={`${issueFilterControlClass} h-8 w-auto text-xs font-medium`} onChange={(event) => onChange({ ...value, condition: event.target.value as "and" | "or" })} value={value.condition}>
        <option value="and">All (AND)</option><option value="or">Any (OR)</option>
      </select>of these conditions
    </div>
    <div className="space-y-2">{value.items.map((item, index) => <div className="flex min-w-0 items-start gap-2" key={index}>
      <div className="min-w-0 flex-1">{isIssueFilterGroup(item)
        ? <GroupEditor count={count} depth={depth + 1} label={`${label}, group ${index + 1}`} onChange={(group) => replace(index, group)} value={item} />
        : <PredicateEditor label={`${label}, condition ${index + 1}`} onChange={(predicate) => replace(index, predicate)} value={item} />}</div>
      <Button aria-label={`Remove ${isIssueFilterGroup(item) ? "group" : "condition"} ${index + 1} from ${label}`} className="luminous-focus mt-1 shrink-0 text-[var(--ls-text-tertiary)]" onClick={() => replace(index)} size="icon" type="button" variant="ghost"><Trash2 className="size-3.5" /></Button>
    </div>)}</div>
    {!value.items.length ? <p className="rounded-[12px] bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">No additional conditions. The selected inbox view still applies.</p> : null}
    <div className="mt-3 flex flex-wrap gap-2">
      <Button className={issueFilterButtonClass} disabled={count >= 20} onClick={() => onChange({ ...value, items: [...value.items, defaultPredicate()] })} type="button" variant="outline"><Plus className="size-3.5" />Condition</Button>
      {depth === 1 ? <Button className={issueFilterButtonClass} disabled={count >= 20} onClick={() => onChange({ ...value, items: [...value.items, { condition: "or", items: [defaultPredicate()] }] })} type="button" variant="outline"><Plus className="size-3.5" />AND / OR group</Button> : null}
    </div>
  </fieldset>;
}

function PredicateEditor({ value, onChange, label }: { value: IssueFilterPredicate; onChange: (value: IssueFilterPredicate) => void; label: string }) {
  const field = issueFilterFields.find((field) => field.value === value.field)!;
  return <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(100px,.9fr)_minmax(100px,.9fr)_minmax(100px,1.2fr)]">
    <select aria-label={`${label} field`} className={issueFilterControlClass} onChange={(event) => {
      const next = issueFilterFields.find((field) => field.value === event.target.value)!;
      onChange({ field: next.value, operator: next.operators[0], value: next.options?.[0] ?? "" });
    }} value={value.field}>{issueFilterFields.map((field) => <option key={field.value} value={field.value}>{field.label}</option>)}</select>
    <select aria-label={`${label} operator`} className={issueFilterControlClass} onChange={(event) => onChange({ ...value, operator: event.target.value as IssueFilterPredicate["operator"] })} value={value.operator}>{field.operators.map((operator) => <option key={operator} value={operator}>{issueFilterOperatorLabels[operator]}</option>)}</select>
    {field.options ? <select aria-label={`${label} value`} className={issueFilterControlClass} onChange={(event) => onChange({ ...value, value: event.target.value })} value={value.value}>{field.options.map((option) => <option key={option} value={option}>{option === "me" ? "Assigned to me" : option === "unassigned" ? "Unassigned" : option}</option>)}</select>
      : <input aria-label={`${label} value`} className={issueFilterControlClass} maxLength={2048} onChange={(event) => onChange({ ...value, value: event.target.value })} placeholder={field.placeholder} value={value.value} />}
  </div>;
}
