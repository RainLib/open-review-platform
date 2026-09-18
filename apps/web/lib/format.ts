import type { RunState } from "@/lib/control-api";

export function formatTime(value?: string) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

export function shortSHA(value: string) {
  return value.slice(0, 8);
}

export function displayRunState(state: RunState) {
  return state.replaceAll("_", " ");
}

export function isActiveRun(state: RunState) {
  return [
    "acknowledged",
    "admitted",
    "preparing",
    "analyzing",
    "normalizing",
    "publishing",
  ].includes(state);
}

export function isAttentionRun(state: RunState) {
  return state === "failed" || state === "needs_attention";
}
