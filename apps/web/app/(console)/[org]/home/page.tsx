import Link from "next/link";
import {
  ArrowRight,
  CircleAlert,
  Clock3,
  GitPullRequest,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import { StatusBadge } from "@/components/console/status-badge";
import {
  DataFreshness,
  PageState,
  RecoveryAction,
} from "@/components/console/page-state";
import { getConsoleData, getPullRequestData } from "@/lib/control-api";
import { cn } from "@/lib/utils";
import {
  formatTime,
  shortSHA,
} from "@/lib/format";

export default async function HomePage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const [data, pullRequests] = await Promise.all([
    getConsoleData(org),
    getPullRequestData(org, { view: "all", limit: 6 }),
  ]);
  const reviewIndexLive = pullRequests.source === "live" || pullRequests.source === "demo";
  const governanceAvailable = data.source === "live" || data.source === "demo";
  const reviewSource = pullRequests.source;
  const reviewRuns = reviewIndexLive ? pullRequests.runs : [];
  const hasVerifiedInstallation = governanceAvailable && data.installations.some(
    (installation) => installation.verification_state === "verified",
  );

  return (
    <div className="space-y-7">
      <header className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Evidence-first operations
          </p>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
            Review cockpit
          </h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Decide where attention belongs next. Counts are resolved from one
            current durable review per pull request, never a slice of retries.
          </p>
        </div>
        <DataFreshness
          detail={reviewIndexLive && !governanceAvailable
            ? data.detail ?? "Review counts are current, but connection and policy inventory could not be verified."
            : pullRequests.detail}
          state={
            reviewSource === "live"
              ? governanceAvailable ? "live" : "partial"
              : reviewSource === "demo"
                ? "demo"
                : "unavailable"
          }
        />
      </header>

      <section className="grid gap-3 md:grid-cols-3">
        <Metric
          detail="Current pull requests that are queued, running, or awaiting a durable transition"
          icon={Clock3}
          label="Active reviews"
          tone="accent"
          value={reviewIndexLive ? pullRequests.counts.active : undefined}
        />
        <Metric
          detail="Current pull requests whose latest review needs an explicit decision"
          icon={CircleAlert}
          label="Needs attention"
          tone="warning"
          value={reviewIndexLive ? pullRequests.counts.attention : undefined}
        />
        <Metric
          detail="Current pull requests with a terminal review state"
          icon={ShieldCheck}
          label="Closed reviews"
          tone="success"
          value={reviewIndexLive ? pullRequests.counts.completed : undefined}
        />
      </section>

      {reviewRuns.length ? (
        <div className="grid gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.75fr)]">
          <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
            <div className="flex items-center justify-between gap-4 border-b border-[var(--ls-line)] px-5 py-4">
              <div>
                <h2 className="text-sm font-semibold text-[var(--ls-text)]">
                  Review pulse
                </h2>
                <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
                  One current review per pull request, ordered by control-plane time.
                </p>
              </div>
              <Link
                className="luminous-focus inline-flex min-h-6 shrink-0 items-center gap-1 rounded text-xs font-medium text-[var(--ls-accent)]"
                href={`/${encodeURIComponent(org)}/reviews`}
              >
                All reviews <ArrowRight className="size-3.5" />
              </Link>
            </div>
            <div className="divide-y divide-[var(--ls-line)]">
              {reviewRuns.map((run) => (
                <Link
                  className="luminous-focus group flex flex-col gap-3 px-5 py-4 transition hover:bg-[var(--ls-surface-muted)] sm:flex-row sm:items-center sm:justify-between"
                  href={`/${encodeURIComponent(org)}/tasks/${encodeURIComponent(run.id)}?tab=overview`}
                  key={run.id}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <GitPullRequest className="size-3.5 shrink-0 text-[var(--ls-text-tertiary)] group-hover:text-[var(--ls-accent)]" />
                      <p className="truncate text-sm font-medium text-[var(--ls-text)]">
                        {run.title || `${run.repository} #${run.review_number}`}
                      </p>
                    </div>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--ls-text-tertiary)]">
                      <span>
                        {run.repository} #{run.review_number}{run.author ? ` · ${run.author}` : ""}
                      </span>
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
          </section>

          <section className="luminous-frosted rounded-[20px] border border-[var(--ls-line-strong)] p-5 shadow-[var(--ls-shadow-float)]">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
              <Sparkles className="size-4 text-[var(--ls-accent)]" />
              Governance signal
            </div>
            <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">
              Policy resolves into an immutable snapshot at admission. A pull
              request cannot rewrite its governance from the branch under review.
            </p>
            <div className="mt-6 space-y-3">
              {governanceAvailable ? (
                <Signal
                  detail="Rules are versioned and require explicit publication."
                  icon={ShieldCheck}
                  label={`${data.ruleSets.length} policy set${data.ruleSets.length === 1 ? "" : "s"} visible`}
                  tone="success"
                />
              ) : (
                <Signal
                  detail="The policy inventory could not be verified. No rule-set count is inferred."
                  icon={CircleAlert}
                  label="Policy inventory unavailable"
                  tone="warning"
                />
              )}
              <Signal
                detail="Only the exact reviewed revision is authoritative."
                icon={Clock3}
                label="Revision-aware history"
                tone="accent"
              />
              {reviewIndexLive && pullRequests.counts.attention ? (
                <Signal
                  detail="Review exact run evidence before changing policy."
                  icon={CircleAlert}
                  label={`${pullRequests.counts.attention} follow-up${pullRequests.counts.attention === 1 ? "" : "s"} need attention`}
                  tone="warning"
                />
              ) : null}
            </div>
            <Link
              className="luminous-focus mt-6 inline-flex min-h-6 items-center gap-1 rounded text-sm font-medium text-[var(--ls-accent)]"
              href={`/${encodeURIComponent(org)}/tasks?tab=needs-attention`}
            >
              Open work queue <ArrowRight className="size-4" />
            </Link>
          </section>
        </div>
      ) : reviewIndexLive && !governanceAvailable ? (
        <PageState
          action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=installations`}>Check connections</RecoveryAction>}
          detail="The pull-request index has no current review runs, but connection and policy status could not be verified. No first-use state is inferred."
          kind="partial"
          title="Review index loaded; setup status unavailable"
        />
      ) : reviewIndexLive ? (
        <PageState
          action={
            <RecoveryAction
              href={`/${encodeURIComponent(org)}/connect?tab=installations`}
              variant="primary"
            >
              {hasVerifiedInstallation ? "Open connections" : "Connect source control"}
            </RecoveryAction>
          }
          detail={`${hasVerifiedInstallation
            ? "Your connection is verified. Open or update an eligible pull request to create the first durable review run."
            : "Connect a provider and admit a pull request to see durable review state here."} The cockpit does not synthesize placeholder activity.`}
          kind="first-use-empty"
          title="No review activity is available"
        />
      ) : (
        <PageState
          action={<RecoveryAction href={`/${encodeURIComponent(org)}/reviews?view=all`}>Open pull requests</RecoveryAction>}
          detail={pullRequests.detail ?? "The current pull-request index could not be loaded. No review counts are shown until the control-plane read succeeds."}
          kind="unavailable"
          title="Review index is unavailable"
        />
      )}
    </div>
  );
}

function Metric({
  detail,
  icon: Icon,
  label,
  tone,
  value,
}: {
  detail: string;
  icon: typeof Clock3;
  label: string;
  tone: "accent" | "warning" | "success";
  value?: number;
}) {
  const toneClass = {
    accent: "text-[var(--ls-accent)]",
    warning: "text-[var(--ls-warning-text)]",
    success: "text-[var(--ls-success-text)]",
  }[tone];
  return (
    <div className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
      <div className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]">
        <Icon className={cn("size-4", toneClass)} />
        {label}
      </div>
      <p className="mt-4 text-2xl font-semibold tracking-tight text-[var(--ls-text)]">
        {value ?? "—"}
      </p>
      <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
        {detail}
      </p>
    </div>
  );
}

function Signal({
  detail,
  icon: Icon,
  label,
  tone,
}: {
  detail: string;
  icon: typeof ShieldCheck;
  label: string;
  tone: "accent" | "warning" | "success";
}) {
  const toneClass = {
    accent: "text-[var(--ls-accent)]",
    warning: "text-[var(--ls-warning-text)]",
    success: "text-[var(--ls-success-text)]",
  }[tone];
  return (
    <div className="flex gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
      <Icon className={cn("mt-0.5 size-4 shrink-0", toneClass)} />
      <div>
        <p className="text-xs font-medium text-[var(--ls-text)]">{label}</p>
        <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
          {detail}
        </p>
      </div>
    </div>
  );
}
