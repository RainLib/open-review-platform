import { cn } from "@/lib/utils";
import { displayRunState } from "@/lib/format";
import type { RunState } from "@/lib/control-api";

const stateStyle: Record<RunState, string> = {
  acknowledged: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
  admitted: "border-sky-500/25 bg-sky-500/[0.08] text-sky-700 dark:text-sky-300",
  preparing: "border-violet-500/25 bg-violet-500/[0.08] text-violet-700 dark:text-violet-300",
  analyzing: "border-[color:color-mix(in_srgb,var(--ls-accent)_30%,transparent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]",
  normalizing: "border-indigo-500/25 bg-indigo-500/[0.08] text-indigo-700 dark:text-indigo-300",
  publishing: "border-blue-500/25 bg-blue-500/[0.08] text-blue-700 dark:text-blue-300",
  completed: "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_10%,transparent)] text-[var(--ls-success-text)]",
  failed: "border-[color:color-mix(in_srgb,var(--ls-critical)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-critical)_9%,transparent)] text-[var(--ls-critical-text)]",
  cancelled: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
  superseded: "border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
  needs_attention: "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_9%,transparent)] text-[var(--ls-warning-text)]",
};

export function StatusBadge({ state }: { state: RunState }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium capitalize",
        stateStyle[state],
      )}
    >
      <span className="size-1.5 rounded-full bg-current" />
      {displayRunState(state)}
    </span>
  );
}
