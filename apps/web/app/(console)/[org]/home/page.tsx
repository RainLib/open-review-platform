import Link from "next/link";
import {
  ArrowRight,
  CircleAlert,
  Clock3,
  GitPullRequest,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import {
  DataSourceNotice,
  EmptyData,
} from "@/components/console/data-source-notice";
import { PageTitle } from "@/components/console/console-shell";
import { StatusBadge } from "@/components/console/status-badge";
import { getConsoleData } from "@/lib/control-api";
import {
  formatTime,
  isActiveRun,
  isAttentionRun,
  shortSHA,
} from "@/lib/format";

function Stat({
  label,
  value,
  detail,
  tone = "default",
}: {
  label: string;
  value: number;
  detail: string;
  tone?: "default" | "attention" | "active";
}) {
  const toneClass =
    tone === "attention"
      ? "text-amber-200"
      : tone === "active"
        ? "text-cyan-200"
        : "text-zinc-100";
  return (
    <div className="rounded-2xl border border-white/[0.075] bg-console-surface p-5 shadow-console-surface">
      <p className="text-xs font-medium text-zinc-500">{label}</p>
      <div
        className={`mt-2 text-3xl font-semibold tracking-[-0.05em] ${toneClass}`}
      >
        {value}
      </div>
      <p className="mt-2 text-xs leading-5 text-zinc-500">{detail}</p>
    </div>
  );
}

export default async function HomePage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const data = await getConsoleData(org);
  const activeRuns = data.runs.filter((run) => isActiveRun(run.state));
  const attentionRuns = data.runs.filter((run) => isAttentionRun(run.state));
  const completedRuns = data.runs.filter((run) => run.state === "completed");

  return (
    <div className="space-y-7">
      <div className="flex flex-col justify-between gap-5 xl:flex-row xl:items-end">
        <PageTitle
          eyebrow="Review intelligence"
          title="Good morning, RainLib."
          description="See where review work is moving, which policy decisions need attention, and which revision is currently being evaluated."
        />
        <div className="w-full xl:max-w-md">
          <DataSourceNotice data={data} />
        </div>
      </div>

      <section className="grid gap-3 sm:grid-cols-3">
        <Stat
          label="Reviewing now"
          value={activeRuns.length}
          detail="Runs are progressing through the durable review pipeline."
          tone="active"
        />
        <Stat
          label="Needs a decision"
          value={attentionRuns.length}
          detail="Runs needing human attention before a final outcome."
          tone="attention"
        />
        <Stat
          label="Completed"
          value={completedRuns.length}
          detail="Completed among the latest control-plane review runs."
        />
      </section>

      {data.runs.length === 0 ? (
        <EmptyData
          title="No review activity is available"
          detail="Connect the local control plane for development, or configure the future Casdoor session bridge for an authenticated deployment."
        />
      ) : (
        <section className="grid gap-5 xl:grid-cols-[minmax(0,1.55fr)_minmax(290px,0.8fr)]">
          <div className="overflow-hidden rounded-2xl border border-white/[0.075] bg-console-surface">
            <div className="flex items-center justify-between border-b border-white/[0.075] px-5 py-4">
              <div>
                <p className="text-sm font-medium text-zinc-100">
                  Review pulse
                </p>
                <p className="mt-1 text-xs text-zinc-500">
                  Latest durable executions, ordered by the control plane.
                </p>
              </div>
              <Link
                className="flex items-center gap-1 text-xs font-medium text-cyan-200 hover:text-cyan-100"
                href={`/${org}/reviews`}
              >
                All reviews <ArrowRight className="size-3.5" />
              </Link>
            </div>
            <div className="divide-y divide-white/[0.06]">
              {data.runs.slice(0, 5).map((run) => (
                <Link
                  className="group flex flex-col gap-3 px-5 py-4 transition hover:bg-white/[0.025] sm:flex-row sm:items-center sm:justify-between"
                  href={`/${org}/tasks/${run.id}`}
                  key={run.id}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <GitPullRequest className="size-3.5 shrink-0 text-zinc-500 group-hover:text-cyan-300" />
                      <p className="truncate text-sm font-medium text-zinc-200">
                        {run.repository}
                        <span className="ml-1.5 text-zinc-500">
                          #{run.review_number}
                        </span>
                      </p>
                    </div>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-zinc-500">
                      <span className="font-mono">
                        {shortSHA(run.base_sha)} → {shortSHA(run.head_sha)}
                      </span>
                      <span>{run.trigger_kind.replaceAll("_", " ")}</span>
                      <span>{formatTime(run.created_at)}</span>
                    </div>
                  </div>
                  <StatusBadge state={run.state} />
                </Link>
              ))}
            </div>
          </div>

          <div className="rounded-2xl border border-white/[0.075] bg-[radial-gradient(ellipse_at_top,var(--console-accent-glow),transparent_60%),var(--console-surface)] p-5">
            <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
              <Sparkles className="size-4 text-cyan-300" />
              Governance signal
            </div>
            <p className="mt-2 text-sm leading-6 text-zinc-400">
              Policy is resolved into an immutable snapshot at admission. A pull
              request cannot rewrite its governance from the branch under
              review.
            </p>
            <div className="mt-6 space-y-3">
              <div className="flex gap-3 rounded-xl border border-white/[0.06] bg-black/15 p-3">
                <ShieldCheck className="mt-0.5 size-4 shrink-0 text-emerald-300" />
                <div>
                  <p className="text-xs font-medium text-zinc-200">
                    {data.ruleSets.length} policy set
                    {data.ruleSets.length === 1 ? "" : "s"} visible
                  </p>
                  <p className="mt-1 text-xs leading-5 text-zinc-500">
                    Rules are versioned and require explicit publication.
                  </p>
                </div>
              </div>
              <div className="flex gap-3 rounded-xl border border-white/[0.06] bg-black/15 p-3">
                <Clock3 className="mt-0.5 size-4 shrink-0 text-cyan-300" />
                <div>
                  <p className="text-xs font-medium text-zinc-200">
                    Revision-aware history
                  </p>
                  <p className="mt-1 text-xs leading-5 text-zinc-500">
                    Superseded runs remain traceable; only the exact reviewed
                    revision is authoritative.
                  </p>
                </div>
              </div>
              {attentionRuns.length > 0 ? (
                <div className="flex gap-3 rounded-xl border border-amber-300/10 bg-amber-300/[0.045] p-3">
                  <CircleAlert className="mt-0.5 size-4 shrink-0 text-amber-300" />
                  <div>
                    <p className="text-xs font-medium text-amber-100">
                      {attentionRuns.length} follow-up
                      {attentionRuns.length === 1 ? "" : "s"} need attention
                    </p>
                    <p className="mt-1 text-xs leading-5 text-amber-100/60">
                      Review their exact run state and evidence before changing
                      policy.
                    </p>
                  </div>
                </div>
              ) : null}
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
