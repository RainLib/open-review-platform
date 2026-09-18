import { cn } from "@/lib/utils";
import { displayRunState } from "@/lib/format";
import type { RunState } from "@/lib/control-api";

const stateStyle: Record<RunState, string> = {
  acknowledged: "border-slate-500/30 bg-slate-500/10 text-slate-300",
  admitted: "border-sky-400/30 bg-sky-400/10 text-sky-200",
  preparing: "border-violet-400/30 bg-violet-400/10 text-violet-200",
  analyzing: "border-cyan-400/30 bg-cyan-400/10 text-cyan-200",
  normalizing: "border-indigo-400/30 bg-indigo-400/10 text-indigo-200",
  publishing: "border-blue-400/30 bg-blue-400/10 text-blue-200",
  completed: "border-emerald-400/30 bg-emerald-400/10 text-emerald-200",
  failed: "border-rose-400/30 bg-rose-400/10 text-rose-200",
  cancelled: "border-zinc-500/30 bg-zinc-500/10 text-zinc-300",
  superseded: "border-zinc-500/30 bg-zinc-500/10 text-zinc-300",
  needs_attention: "border-amber-400/30 bg-amber-400/10 text-amber-200",
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
