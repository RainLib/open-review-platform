export const issueViews = ["all", "open", "regressed", "critical", "assigned", "resolved", "suppressed"] as const;
export type IssueView = typeof issueViews[number];
export type IssueFilterField = "status" | "severity" | "category" | "repository" | "provider" | "api_base_url" | "path" | "assignee" | "rule" | "query" | "age";
export type IssueFilterOperator = "is" | "is_not" | "contains" | "not_contains" | "within" | "not_within";
export type IssueFilterPredicate = { field: IssueFilterField; operator: IssueFilterOperator; value: string };
export type IssueFilterGroup = { condition: "and" | "or"; items: Array<IssueFilterGroup | IssueFilterPredicate> };
export type IssueViewDefinition = { view: IssueView; filters: IssueFilterGroup };
export type IssueSavedView = {
  id: string; name: string; visibility: "personal" | "workspace"; revision: number;
  definition: IssueViewDefinition; can_manage: boolean;
};
export type IssueQuery = {
  view?: string; filters?: string; saved_view?: string; filter_time?: string;
  query?: string; severity?: string; status?: string; category?: string; repository?: string;
  provider?: string; api_base_url?: string; path?: string; assignee?: string; rule?: string;
  age?: string; since?: string; selected?: string; cursor?: string; direction?: "after" | "before";
};

const textOperators: IssueFilterOperator[] = ["is", "is_not", "contains", "not_contains"];
export const issueFilterFields: Array<{ value: IssueFilterField; label: string; operators: IssueFilterOperator[]; options?: string[]; placeholder?: string }> = [
  { value: "status", label: "Status", operators: ["is", "is_not"], options: ["open", "regressed", "resolved", "suppressed"] },
  { value: "severity", label: "Severity", operators: ["is", "is_not"], options: ["critical", "high", "medium", "low"] },
  { value: "category", label: "Category", operators: textOperators, placeholder: "Security" },
  { value: "repository", label: "Repository", operators: textOperators, placeholder: "team/service" },
  { value: "provider", label: "Git provider", operators: ["is", "is_not"], options: ["github", "gitlab"] },
  { value: "api_base_url", label: "Provider instance", operators: textOperators, placeholder: "https://gitlab.internal/api/v4" },
  { value: "path", label: "File path", operators: textOperators, placeholder: "src/auth/" },
  { value: "assignee", label: "Assignee", operators: ["is", "is_not"], options: ["me", "unassigned"] },
  { value: "rule", label: "Rule", operators: textOperators, placeholder: "security.authentication" },
  { value: "query", label: "Search text", operators: textOperators, placeholder: "Missing authorization" },
  { value: "age", label: "Last seen", operators: ["within", "not_within"], options: ["24h", "7d", "30d"] },
];
export const issueFilterOperatorLabels: Record<IssueFilterOperator, string> = {
  is: "is", is_not: "is not", contains: "contains", not_contains: "does not contain", within: "within", not_within: "not within",
};
export const emptyIssueFilters = (): IssueFilterGroup => ({ condition: "and", items: [] });
export const isIssueFilterGroup = (item: IssueFilterGroup | IssueFilterPredicate): item is IssueFilterGroup => "condition" in item;
export const countIssueFilters = (group: IssueFilterGroup): number => group.items.reduce((count, item) => count + (isIssueFilterGroup(item) ? countIssueFilters(item) : 1), 0);

export function validateIssueFilters(input: unknown): IssueFilterGroup {
  let count = 0;
  function visit(value: unknown, depth: number): IssueFilterGroup | IssueFilterPredicate {
    if (depth > 3) throw new Error("Filters support a root group, one nested group, and its conditions.");
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Each filter must be a condition or a group.");
    const record = value as Record<string, unknown>;
    if ("condition" in record) {
      if (Object.keys(record).some((key) => key !== "condition" && key !== "items") || !["and", "or"].includes(String(record.condition)) || !Array.isArray(record.items)) throw new Error("A filter group needs AND or OR and a list of conditions.");
      if (record.items.length === 0 && (depth !== 1 || record.condition !== "and")) throw new Error("Add a condition to each group or remove the empty group.");
      return { condition: record.condition as "and" | "or", items: record.items.map((item) => visit(item, depth + 1)) };
    }
    if (depth === 1) throw new Error("Filters must start with an AND or OR group.");
    if (Object.keys(record).some((key) => !["field", "operator", "value"].includes(key))) throw new Error("A filter contains unsupported properties.");
    const field = issueFilterFields.find((field) => field.value === record.field);
    if (!field || !field.operators.includes(record.operator as IssueFilterOperator)) throw new Error("Choose a supported field and operator.");
    if (typeof record.value !== "string" || !record.value.trim()) throw new Error(`Enter a value for ${field.label.toLowerCase()}.`);
    const text = record.value.trim();
    if (text.includes("\0") || new TextEncoder().encode(text).length > 512) throw new Error("Each filter value must be at most 512 bytes and cannot contain NUL characters.");
    if (field.options && !field.options.includes(text)) throw new Error(`Choose a valid ${field.label.toLowerCase()} value.`);
    if (++count > 20) throw new Error("Use no more than 20 filter conditions.");
    return { field: field.value, operator: record.operator as IssueFilterOperator, value: text };
  }
  const group = visit(input, 1) as IssueFilterGroup;
  if (new TextEncoder().encode(JSON.stringify(group)).length > 8192) throw new Error("The filter is too large. Shorten its values (8 KB maximum).");
  return group;
}

export function parseIssueFilters(value?: string): IssueFilterGroup {
  if (!value) return emptyIssueFilters();
  if (new TextEncoder().encode(value).length > 8192) throw new Error("The filter is too large (8 KB maximum).");
  let parsed: unknown;
  try { parsed = JSON.parse(value); } catch { throw new Error("The filter link contains invalid JSON. Edit or clear the filters to continue."); }
  return validateIssueFilters(parsed);
}

export function issueDefinitionFromQuery(query: IssueQuery): IssueViewDefinition {
  const view = query.view || "open";
  if (!issueViews.includes(view as IssueView)) throw new Error("This issue view is not supported. Choose a built-in or saved view.");
  const group = parseIssueFilters(query.filters);
  const flat: IssueFilterPredicate[] = [];
  for (const field of ["status", "severity", "category", "repository", "provider", "api_base_url", "path", "assignee", "rule", "query", "age"] as const) {
    if (query[field]) flat.push({ field, operator: field === "age" ? "within" : field === "query" ? "contains" : "is", value: query[field]! });
  }
  if (query.since && !query.age) throw new Error("This legacy date filter cannot be saved. Choose a last-seen period or clear filters.");
  const filters = flat.length ? { condition: "and" as const, items: [...(group.condition === "and" ? group.items : [group]), ...flat] } : group;
  return { view: view as IssueView, filters: validateIssueFilters(filters) };
}

export function issueDefinitionKey(definition: IssueViewDefinition): string {
  return JSON.stringify({ view: definition.view, filters: validateIssueFilters(definition.filters) });
}

// Only filter intent is transferred. A new definition must not inherit a stale
// cursor, row selection, relative-time anchor, or legacy flat predicate.
export function issueDefinitionHref(org: string, definition: IssueViewDefinition, savedViewID?: string): string {
  const filters = validateIssueFilters(definition.filters);
  const search = new URLSearchParams({ view: definition.view });
  if (filters.items.length) search.set("filters", JSON.stringify(filters));
  if (savedViewID) search.set("saved_view", savedViewID);
  return `/${encodeURIComponent(org)}/issues?${search}`;
}

export function issueFilterAnchor(query: IssueQuery): string | undefined {
  if (query.filter_time) {
    if (!/^\d{4}-\d{2}-\d{2}T/.test(query.filter_time) || Number.isNaN(Date.parse(query.filter_time))) throw new Error("The filter time is invalid. Apply the filters again.");
    return query.filter_time;
  }
  if (query.since && query.age) {
    const duration = ({ "24h": 1, "7d": 7, "30d": 30 } as Record<string, number>)[query.age];
    if (!duration || Number.isNaN(Date.parse(query.since))) throw new Error("The legacy date filter is invalid. Apply the filters again.");
    return new Date(Date.parse(query.since) + duration * 86400000).toISOString();
  }
  return undefined;
}

export function issueListPreview(body: string): string {
  return body
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/(\*\*|__)(.*?)\1/g, "$2")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

// Review findings have no separate title field. Derive a short display title
// without altering the retained evidence shown in the inspector and detail.
export function issueDisplayTitle(body: string): string {
  const preview = issueListPreview(body);
  const firstSentence = preview.match(/^.{1,180}?[.!?](?=\s|$)/)?.[0] ?? preview;
  if (firstSentence.length <= 140) return firstSentence;
  const prefix = firstSentence.slice(0, 140);
  const wordEnd = prefix.lastIndexOf(" ");
  return `${prefix.slice(0, wordEnd > 80 ? wordEnd : 140).trimEnd()}…`;
}

// Keep the page header and inspector concise. The exact retained finding is
// rendered separately in Evidence; this text is only navigation context.
export function issueDisplaySummary(body: string): string {
  const preview = issueListPreview(body);
  const title = issueDisplayTitle(body);
  const explanation = preview.startsWith(title) ? preview.slice(title.length).trim() : preview;
  const firstSentence = explanation.match(/^.{1,300}?[.!?](?=\s|$)/)?.[0] ?? explanation;
  if (firstSentence.length <= 240) return firstSentence || title;
  const prefix = firstSentence.slice(0, 240);
  const wordEnd = prefix.lastIndexOf(" ");
  return `${prefix.slice(0, wordEnd > 150 ? wordEnd : 240).trimEnd()}…`;
}
