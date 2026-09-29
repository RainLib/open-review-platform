import type { RunEvent } from "./control-api";

export function isRunEvent(value: unknown, runID: string): value is RunEvent {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const event = value as Partial<RunEvent>;
  return typeof event.id === "string" && event.id.length > 0
    && event.run_id === runID
    && typeof event.revision === "number" && Number.isSafeInteger(event.revision) && event.revision >= 0
    && typeof event.event_type === "string" && event.event_type.length > 0
    && typeof event.actor_kind === "string" && event.actor_kind.length > 0
    && (event.actor_subject === undefined || typeof event.actor_subject === "string")
    && typeof event.created_at === "string" && !Number.isNaN(Date.parse(event.created_at))
    && Boolean(event.payload) && typeof event.payload === "object" && !Array.isArray(event.payload);
}

export function mergeRunEvents(current: RunEvent[], incoming: RunEvent[]): RunEvent[] {
  const byID = new Map<string, RunEvent>();
  for (const event of [...current, ...incoming]) byID.set(event.id, event);
  return [...byID.values()].sort((left, right) =>
    left.revision - right.revision
    || left.created_at.localeCompare(right.created_at)
    || left.id.localeCompare(right.id));
}

export function filterRunEvents(events: RunEvent[], query: string, actor: string): RunEvent[] {
  const term = query.trim().toLocaleLowerCase().slice(0, 80);
  return events.filter((event) => {
    if (actor !== "all" && event.actor_kind !== actor) return false;
    if (!term) return true;
    return [event.event_type, event.actor_kind, event.actor_subject ?? "", String(event.revision)]
      .some((value) => value.toLocaleLowerCase().includes(term));
  });
}

const displayableKeys = new Set([
  "provider", "delivery_id", "review_number", "trigger", "review_mode", "mode",
  "configuration_revision", "rule_snapshot", "finding_count", "highest_severity",
  "merge_gate", "status_receipt", "failure_code", "legacy_job_id", "interaction_id",
  "replacement_run_id", "replacement_head_sha", "retry_after", "snapshot", "code",
]);

export function visibleRunEventPayload(payload: Record<string, unknown>): Record<string, unknown> {
  const visible: Record<string, unknown> = {};
  const entries = Object.entries(payload);
  let omitted = 0;
  for (const [key, value] of entries) {
    if (!displayableKeys.has(key) || !["string", "number", "boolean"].includes(typeof value)) {
      omitted += 1;
      continue;
    }
    visible[key] = typeof value === "string" ? value.slice(0, 160) : value;
  }
  if (omitted) visible["Additional metadata"] = `${omitted} non-display fields omitted`;
  return visible;
}
