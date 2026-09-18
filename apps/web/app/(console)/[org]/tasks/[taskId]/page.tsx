import Link from "next/link";
import {
  ArrowLeft,
  Braces,
  ExternalLink,
  FileKey2,
  GitCommitHorizontal,
  GitPullRequest,
  Layers3,
  Timer,
} from "lucide-react";

import {
  DataSourceNotice,
  EmptyData,
} from "@/components/console/data-source-notice";
import { RunControls } from "@/components/console/run-controls";
import { RunJourney } from "@/components/console/run-journey";
import { StatusBadge } from "@/components/console/status-badge";
import { getDemoRunEvents, getRunDetail } from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";

export default async function TaskDetailPage({
  params,
}: {
  params: Promise<{ org: string; taskId: string }>;
}) {
  const { org, taskId } = await params;
  const data = await getRunDetail(org, taskId);
  const run = data.run;

  if (!run) {
    return (
      <div className="space-y-7">
        <Link
          className="inline-flex items-center gap-2 text-sm text-zinc-400 hover:text-zinc-100"
          href={`/${org}/tasks`}
        >
          <ArrowLeft className="size-4" />
          Back to work queue
        </Link>
        <EmptyData
          title="This review run is unavailable"
          detail={
            data.detail ??
            "It may have been removed, or you may not have access to this tenant."
          }
        />
      </div>
    );
  }

  const events = data.source === "demo" ? getDemoRunEvents(run.id) : [];
  const startedAt = run.started_at ?? run.created_at;
  const duration = run.finished_at
    ? `${Math.max(0, Math.round((new Date(run.finished_at).getTime() - new Date(startedAt).getTime()) / 1000))}s`
    : "In progress";

  return (
    <div className="space-y-7">
      <div className="flex items-center justify-between">
        <Link
          className="inline-flex items-center gap-2 text-sm text-zinc-400 hover:text-zinc-100"
          href={`/${org}/tasks`}
        >
          <ArrowLeft className="size-4" />
          Back to work queue
        </Link>
        <DataSourceNotice data={data} />
      </div>
      <section className="rounded-[28px] border border-white/[0.075] bg-[radial-gradient(ellipse_at_top_left,var(--console-accent-glow),transparent_52%),var(--console-surface)] p-5 sm:p-7">
        <div className="flex flex-col justify-between gap-6 xl:flex-row xl:items-start">
          <div className="min-w-0">
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <StatusBadge state={run.state} />
              <span className="rounded-full border border-white/[0.08] bg-black/10 px-2 py-0.5 font-mono text-xs text-zinc-400">
                r{run.revision}
              </span>
            </div>
            <div className="flex items-start gap-3">
              <GitPullRequest className="mt-1 size-5 shrink-0 text-cyan-300" />
              <div>
                <h1 className="text-2xl font-semibold tracking-[-0.04em] text-white sm:text-3xl">
                  {run.repository}
                  <span className="ml-2 text-zinc-500">
                    #{run.review_number}
                  </span>
                </h1>
                <p className="mt-2 text-sm leading-6 text-zinc-400">
                  Triggered by {run.trigger_kind.replaceAll("_", " ")}. The
                  exact head revision and policy snapshot are attached to this
                  durable run.
                </p>
              </div>
            </div>
          </div>
          <RunControls
            enabled={data.source === "live"}
            org={org}
            revision={run.revision}
            runID={run.id}
            state={run.state}
          />
        </div>
        <div className="mt-7 grid gap-3 border-t border-white/[0.075] pt-5 sm:grid-cols-2 xl:grid-cols-4">
          <Meta
            label="Head revision"
            value={shortSHA(run.head_sha)}
            icon={GitCommitHorizontal}
            mono
          />
          <Meta
            label="Base revision"
            value={shortSHA(run.base_sha)}
            icon={GitCommitHorizontal}
            mono
          />
          <Meta label="Started" value={formatTime(startedAt)} icon={Timer} />
          <Meta label="Duration" value={duration} icon={Timer} />
        </div>
      </section>
      <RunJourney
        initialEvents={events}
        initialRevision={run.revision}
        initialState={run.state}
        org={org}
        runID={run.id}
        streamEnabled={data.source === "live"}
      />
      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(300px,0.8fr)]">
        <div className="rounded-[24px] border border-white/[0.075] bg-console-surface p-5">
          <div className="flex items-center gap-2">
            <Layers3 className="size-4 text-cyan-300" />
            <h2 className="text-sm font-medium text-zinc-100">Context</h2>
          </div>
          <dl className="mt-5 divide-y divide-white/[0.06] text-sm">
            <Definition label="Provider" value={run.provider} />
            <Definition
              label="Trigger"
              value={run.trigger_kind.replaceAll("_", " ")}
            />
            <Definition label="Run identifier" value={run.id} mono />
            <Definition
              label="Failure summary"
              value={run.failure_message ?? "No failure recorded."}
            />
          </dl>
        </div>
        <div className="rounded-[24px] border border-white/[0.075] bg-console-surface p-5">
          <div className="flex items-center gap-2">
            <FileKey2 className="size-4 text-cyan-300" />
            <h2 className="text-sm font-medium text-zinc-100">Rule snapshot</h2>
          </div>
          {data.ruleSnapshot ? (
            <div className="mt-5 space-y-4">
              <p className="text-sm leading-6 text-zinc-400">
                Resolved at admission and immutable for this run.
              </p>
              <Definition
                label="Engine"
                value={`${data.ruleSnapshot.engine} · ${data.ruleSnapshot.compiler_version}`}
              />
              <Definition
                label="Snapshot SHA"
                value={data.ruleSnapshot.sha256}
                mono
              />
              <Definition
                label="Sources"
                value={`${data.ruleSnapshot.sources.length} published rule version${data.ruleSnapshot.sources.length === 1 ? "" : "s"}`}
              />
              <Link
                className="inline-flex items-center gap-1.5 text-xs font-medium text-cyan-200 hover:text-cyan-100"
                href={`/${org}/rules`}
              >
                View policy library <ExternalLink className="size-3.5" />
              </Link>
            </div>
          ) : (
            <p className="mt-5 text-sm leading-6 text-zinc-500">
              No rule snapshot is attached to this run yet.
            </p>
          )}
        </div>
      </section>
      <aside className="flex gap-3 rounded-2xl border border-white/[0.07] bg-white/[0.02] p-4 text-xs leading-5 text-zinc-500">
        <Braces className="mt-0.5 size-4 shrink-0 text-zinc-400" />
        The activity stream contains only auditable events and safe operational
        metadata. It never exposes private LLM reasoning.
      </aside>
    </div>
  );
}

function Meta({
  label,
  value,
  icon: Icon,
  mono = false,
}: {
  label: string;
  value: string;
  icon: typeof Timer;
  mono?: boolean;
}) {
  return (
    <div className="flex items-center gap-2.5">
      <Icon className="size-3.5 shrink-0 text-zinc-600" />
      <div className="min-w-0">
        <p className="text-[11px] text-zinc-500">{label}</p>
        <p
          className={`mt-0.5 truncate text-xs text-zinc-200 ${mono ? "font-mono" : ""}`}
        >
          {value}
        </p>
      </div>
    </div>
  );
}

function Definition({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="grid grid-cols-[minmax(100px,0.35fr)_minmax(0,1fr)] gap-4 py-3 first:pt-0 last:pb-0">
      <dt className="text-xs text-zinc-500">{label}</dt>
      <dd
        className={`min-w-0 break-all text-right text-xs leading-5 text-zinc-300 ${mono ? "font-mono" : ""}`}
      >
        {value}
      </dd>
    </div>
  );
}
