import type { ReviewRun } from "@/lib/control-api";

export function reviewDuration(run: Pick<ReviewRun, "started_at" | "finished_at" | "state">): string {
  if (!run.started_at) return "Not started";
  if (!run.finished_at) {
    return ["completed", "failed", "cancelled", "superseded", "needs_attention"].includes(run.state)
      ? "Timing unavailable"
      : "In progress";
  }
  const elapsed = Date.parse(run.finished_at) - Date.parse(run.started_at);
  if (!Number.isFinite(elapsed) || elapsed < 0) return "Timing unavailable";
  const seconds = Math.floor(elapsed / 1000);
  if (seconds === 0) return "<1s";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
