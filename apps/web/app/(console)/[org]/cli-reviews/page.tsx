import Link from "next/link";
import {
  ArrowUpRight,
  CircleAlert,
  Code2,
  GitCommitHorizontal,
  KeyRound,
  ShieldCheck,
  SquareTerminal,
} from "lucide-react";

import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { ProviderMark } from "@/components/providers/provider-icons";
import {
  displayReviewMode,
  getCLIReviewData,
  type CLIReviewRun,
  type ProviderInstallation,
} from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";
import { cliReviewTemplate, configuredPublicControlPlaneURL } from "@/lib/public-control-plane-url";
import { providerCommitTarget, providerRepositoryTarget, providerReviewTarget } from "@/lib/provider-review-url";
import { reviewDuration } from "@/lib/review-duration";
import { cn } from "@/lib/utils";

type CLIReviewTab = "runs" | "quickstart";

export default async function CLIReviewsPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const data = await getCLIReviewData(org);
  const tab: CLIReviewTab = query.tab === "quickstart" ? "quickstart" : "runs";
  const publicAPIURL = configuredPublicControlPlaneURL();

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Machine-triggered review
          </p>
          <h1 className="mt-2 text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
            CLI Reviews
          </h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Trigger the existing durable review workflow for a real pull or
            merge request. The installation, repository allowlist, exact
            revision, rules, model route, and evidence remain pinned to the run.
          </p>
        </div>
        <div className="flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]">
          <span
            className={cn(
              "size-2 rounded-full",
              data.source === "live"
                ? "bg-[var(--ls-success)]"
                : "bg-[var(--ls-warning)]",
            )}
          />
          {data.source === "live"
            ? "Live control-plane data"
            : (data.detail ?? data.source)}
        </div>
      </header>

      <TabStateRouter
        label="CLI review pages"
        className="flex gap-1 border-b border-[var(--ls-line)]"
      >
        <Tab
          href={`/${org}/cli-reviews?tab=runs`}
          label="Runs"
          selected={tab === "runs"}
        />
        <Tab
          href={`/${org}/cli-reviews?tab=quickstart`}
          label="Quickstart"
          selected={tab === "quickstart"}
        />
        <Link
          className="luminous-focus ml-auto mb-2 inline-flex h-9 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-medium text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] hover:text-[var(--ls-text)]"
          href={`/${org}/settings/api-keys`}
        >
          <KeyRound className="size-3.5" /> API keys
        </Link>
      </TabStateRouter>

      {tab === "runs" ? (
        <Runs detail={data.detail} org={org} runs={data.runs} />
      ) : (
        <Quickstart detail={data.detail} installations={data.installations} live={data.source === "live"} org={org} publicAPIURL={publicAPIURL} />
      )}
    </div>
  );
}

function Runs({
  detail,
  org,
  runs,
}: {
  detail?: string;
  org: string;
  runs: CLIReviewRun[];
}) {
  if (runs.length === 0) {
    return (
      <section className="grid min-h-[430px] place-items-center rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center">
        <div className="max-w-xl">
          <SquareTerminal className="mx-auto size-8 text-[var(--ls-text-tertiary)]" />
          <h2 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">
            No CLI review runs yet
          </h2>
          <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
            {detail ??
              "Create a scoped API key, then use Quickstart to review an existing pull or merge request. Arbitrary clone URLs and synthetic PR numbers are not accepted."}
          </p>
          <Link
            className="luminous-focus mt-5 inline-flex h-10 items-center rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)]"
            href={`/${org}/cli-reviews?tab=quickstart`}
          >
            Open quickstart
          </Link>
        </div>
      </section>
    );
  }
  return (
    <>
      <div className="hidden overflow-x-auto rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] md:block">
        <table className="w-full min-w-[1180px] text-left text-sm">
          <thead className="border-b border-[var(--ls-line)] bg-[var(--ls-surface-muted)] text-xs text-[var(--ls-text-tertiary)]">
            <tr>
              <th className="px-5 py-3 font-medium">Pull request</th>
              <th className="px-4 py-3 font-medium">Exact revision</th>
              <th className="px-4 py-3 font-medium">Caller / source</th>
              <th className="px-4 py-3 font-medium">Mode</th>
              <th className="px-4 py-3 font-medium">Duration</th>
              <th className="px-4 py-3 font-medium">Submitted / start</th>
              <th className="px-5 py-3 text-right font-medium">State / evidence</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--ls-line)]">
            {runs.map((run) => (
              <RunRow key={run.id} org={org} run={run} />
            ))}
          </tbody>
        </table>
      </div>
      <div className="grid gap-3 md:hidden">
        {runs.map((run) => (
          <RunCard key={run.id} org={org} run={run} />
        ))}
      </div>
    </>
  );
}

function RunRow({ org, run }: { org: string; run: CLIReviewRun }) {
  const provider = providerReviewTarget(run);
  const repository = providerRepositoryTarget(run);
  const commit = providerCommitTarget(run);
  return (
    <tr className="transition hover:bg-[var(--ls-surface-muted)]">
      <td className="px-5 py-4">
        <div className="flex items-start gap-3">
          <ProviderMark className="mt-0.5 size-4" provider={run.provider} />
          <div>
            {repository ? <a className="luminous-focus rounded font-medium text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={repository.url} rel="noreferrer" target="_blank">{run.repository}<ArrowUpRight className="ml-1 inline size-3" /></a> : <span className="font-medium text-[var(--ls-text)]">{run.repository}</span>}
            <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
              <Link className="luminous-focus rounded text-[var(--ls-accent)] hover:underline" href={`/${org}/cli-reviews/${run.id}`}>#{run.review_number} · evidence</Link>
              {provider ? <a className="luminous-focus rounded text-[var(--ls-text-tertiary)] hover:text-[var(--ls-accent)]" href={provider.url} rel="noreferrer" target="_blank">Open PR/MR</a> : null}
            </div>
          </div>
        </div>
      </td>
      <td className="px-4 py-4">
        {commit ? <a className="luminous-focus inline-flex items-center gap-1 rounded font-mono text-xs text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={commit.url} rel="noreferrer" target="_blank" title={run.head_sha}><GitCommitHorizontal className="size-3.5" />{shortSHA(run.head_sha)}<ArrowUpRight className="size-3" /></a> : <span className="font-mono text-xs text-[var(--ls-text-tertiary)]">Commit unavailable</span>}
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
          r{run.revision}
        </p>
      </td>
      <td className="px-4 py-4 font-mono text-xs text-[var(--ls-text-secondary)]">
        {callerLabel(run.caller_subject)}
        <p className="mt-1 font-sans text-[11px] uppercase tracking-[0.08em] text-[var(--ls-text-tertiary)]">{run.trigger_kind}</p>
      </td>
      <td className="px-4 py-4 capitalize text-[var(--ls-text-secondary)]">
        {displayReviewMode(run.review_mode)}
      </td>
      <td className="px-4 py-4 text-xs text-[var(--ls-text-secondary)]">{reviewDuration(run)}</td>
      <td className="px-4 py-4 text-[var(--ls-text-secondary)]">
        {formatTime(run.created_at)}
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">{run.started_at ? `Started ${formatTime(run.started_at)}` : "Awaiting start"}</p>
      </td>
      <td className="px-5 py-4 text-right">
        <State state={run.state} />
        <Link className="luminous-focus mt-1 block rounded text-xs text-[var(--ls-accent)] hover:underline" href={`/${org}/cli-reviews/${run.id}`}>View evidence</Link>
      </td>
    </tr>
  );
}

function RunCard({ org, run }: { org: string; run: CLIReviewRun }) {
  const repository = providerRepositoryTarget(run);
  const provider = providerReviewTarget(run);
  const commit = providerCommitTarget(run);
  return (
    <article className="box-border min-w-0 w-full rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4 shadow-[var(--ls-shadow-control)]">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <ProviderMark className="size-4" provider={run.provider} />
          {repository ? <a className="luminous-focus truncate rounded text-sm font-semibold text-[var(--ls-text)] hover:text-[var(--ls-accent)]" href={repository.url} rel="noreferrer" target="_blank">{run.repository}</a> : <span className="truncate text-sm font-semibold text-[var(--ls-text)]">{run.repository}</span>}
        </div>
        <State state={run.state} />
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
        <Link className="luminous-focus rounded font-medium text-[var(--ls-accent)] hover:underline" href={`/${org}/cli-reviews/${run.id}`}>#{run.review_number} · View evidence</Link>
        {provider ? <a className="luminous-focus rounded text-[var(--ls-text-tertiary)] hover:text-[var(--ls-accent)]" href={provider.url} rel="noreferrer" target="_blank">Open PR/MR <ArrowUpRight className="inline size-3" /></a> : null}
      </div>
      <div className="mt-4 grid grid-cols-2 gap-3 text-xs text-[var(--ls-text-tertiary)]">
        {commit ? <a className="luminous-focus inline-flex min-w-0 items-center gap-1.5 rounded font-mono text-[var(--ls-text-secondary)] hover:text-[var(--ls-accent)]" href={commit.url} rel="noreferrer" target="_blank" title={run.head_sha}><GitCommitHorizontal className="size-3.5 shrink-0" />{shortSHA(run.head_sha)}<ArrowUpRight className="size-3 shrink-0" /></a> : <span>Commit unavailable</span>}
        <span className="text-right capitalize">
          {displayReviewMode(run.review_mode)}
        </span>
        <span className="font-mono">{callerLabel(run.caller_subject)} · {run.trigger_kind}</span>
        <span className="text-right">{reviewDuration(run)}</span>
        <span>Submitted</span>
        <span className="text-right">{formatTime(run.created_at)}</span>
        <span>Started</span>
        <span className="text-right">{run.started_at ? formatTime(run.started_at) : "Awaiting start"}</span>
      </div>
    </article>
  );
}

function Quickstart({
  detail,
  installations,
  live,
  org,
  publicAPIURL,
}: {
  detail?: string;
  installations: ProviderInstallation[];
  live: boolean;
  org: string;
  publicAPIURL?: string;
}) {
  const installation = installations.find(
    (item) =>
      item.active &&
      (item.verification_state === "legacy" ||
        item.verification_state === "verified"),
  );
  const command = live && installation && publicAPIURL
    ? cliReviewTemplate({
        publicAPIURL,
        org,
        installationID: installation.id,
        repository: installation.repository_scope,
      })
    : undefined;
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(320px,.75fr)]">
      <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7">
        <div className="flex items-start gap-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
            <Code2 className="size-4.5" />
          </span>
          <div>
            <h2 className="text-lg font-semibold text-[var(--ls-text)]">
              Review an existing pull request
            </h2>
            <p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">
              The control plane derives the trusted clone destination from the
              installation. It rejects arbitrary clone URLs, partial revisions,
              unsafe refs, and repositories outside the key allowlist.
            </p>
          </div>
        </div>
        {command ? (
          <div className="mt-6 overflow-hidden rounded-[13px] border border-slate-700 bg-[#101521]">
            <div className="flex items-center justify-between border-b border-slate-700 px-4 py-2.5">
              <span className="text-xs font-medium text-slate-300">Shell · {publicAPIURL}</span>
              <CopyEvidenceButton label="Copy command" value={command} />
            </div>
            <pre className="overflow-x-auto p-4 text-xs leading-6 text-slate-200">
              <code>{command}</code>
            </pre>
          </div>
        ) : (
          <div className="mt-6 rounded-[13px] border border-amber-500/25 bg-amber-500/8 p-4 text-sm leading-6 text-[var(--ls-text-secondary)]">
            {!live ? (
              <>{detail ?? "Live installation data is unavailable. Refresh after the control plane recovers."}</>
            ) : installation ? (
              <>A public API URL is not configured. Set <code className="font-mono">OPEN_REVIEW_PUBLIC_API_URL</code> to the externally reachable HTTPS origin before copying a CLI command.</>
            ) : (
              <>No eligible installation is available. <Link className="font-medium text-[var(--ls-accent)] underline" href={`/${org}/connect`}>Connect and verify a Git provider</Link> before creating a machine review.</>
            )}
          </div>
        )}
        <div className="mt-5 grid gap-3 sm:grid-cols-3">
          <Contract
            icon={ShieldCheck}
            title="Trusted source"
            body="Installation and repository scope are verified server-side."
          />
          <Contract
            icon={GitCommitHorizontal}
            title="Exact revision"
            body="Full base and head SHAs are preserved with the run."
          />
          <Contract
            icon={KeyRound}
            title="Secret-safe"
            body="The API key is read only from the environment."
          />
        </div>
      </section>
      <aside className="space-y-4">
        <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
          <h2 className="text-sm font-semibold text-[var(--ls-text)]">
            Available installations
          </h2>
          <div className="mt-4 space-y-2">
            {installations.length === 0 ? (
              <p className="text-sm leading-6 text-[var(--ls-text-secondary)]">
                No active installation is available. Connect GitHub or GitLab
                before creating a machine review.
              </p>
            ) : (
              installations.map((item) => (
                <div
                  className="rounded-[11px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3"
                  key={item.id}
                >
                  <div className="flex items-center gap-2">
                    <ProviderMark className="size-4" provider={item.provider} />
                    <span className="text-sm font-medium capitalize text-[var(--ls-text)]">
                      {item.provider}
                    </span>
                    <span
                      className={cn(
                        "ml-auto text-[10px] font-semibold uppercase",
                        item.active &&
                          (item.verification_state === "legacy" ||
                            item.verification_state === "verified")
                          ? "text-[var(--ls-success-text)]"
                          : item.active
                            ? "text-[var(--ls-warning-text)]"
                            : "text-[var(--ls-text-tertiary)]",
                      )}
                    >
                      {item.active &&
                      (item.verification_state === "legacy" ||
                        item.verification_state === "verified")
                        ? "Eligible"
                        : item.active
                          ? item.verification_state
                          : "Inactive"}
                    </span>
                  </div>
                  <p className="mt-2 font-mono text-xs text-[var(--ls-text-secondary)]">
                    {item.repository_scope}
                  </p>
                  <p className="mt-1 truncate font-mono text-[10px] text-[var(--ls-text-tertiary)]">
                    {item.id}
                  </p>
                </div>
              ))
            )}
          </div>
        </section>
        <section className="rounded-[16px] border border-amber-500/25 bg-amber-500/8 p-5">
          <div className="flex gap-3">
            <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-warning-text)]" />
            <div>
              <h2 className="text-sm font-semibold text-[var(--ls-text)]">
                Idempotent by default
              </h2>
              <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
                The CLI derives a stable key from tenant, repository, review
                number, exact head SHA, and mode. Repeating the command returns
                the original run; a concurrent request for the same active head
                is coalesced.
              </p>
            </div>
          </div>
        </section>
      </aside>
    </div>
  );
}

function Contract({
  body,
  icon: Icon,
  title,
}: {
  body: string;
  icon: typeof ShieldCheck;
  title: string;
}) {
  return (
    <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
      <Icon className="size-4 text-[var(--ls-accent)]" />
      <h3 className="mt-2 text-xs font-semibold text-[var(--ls-text)]">
        {title}
      </h3>
      <p className="mt-1 text-[11px] leading-5 text-[var(--ls-text-secondary)]">
        {body}
      </p>
    </div>
  );
}

function Tab({
  href,
  label,
  selected,
}: {
  href: string;
  label: string;
  selected: boolean;
}) {
  return (
    <Link
      aria-current={selected ? "page" : undefined}
      aria-selected={selected}
      className={cn(
        "luminous-focus relative inline-flex h-12 items-center px-4 text-sm font-medium",
        selected
          ? "text-[var(--ls-text)]"
          : "text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]",
      )}
      href={href}
      role="tab"
      tabIndex={selected ? 0 : -1}
    >
      {label}
      {selected ? (
        <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" />
      ) : null}
    </Link>
  );
}

function State({ state }: { state: CLIReviewRun["state"] }) {
  const tone =
    state === "completed"
      ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
      : state === "failed" || state === "needs_attention"
        ? "bg-red-500/10 text-[var(--ls-critical-text)]"
        : state === "cancelled" || state === "superseded"
          ? "bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]"
          : "bg-violet-500/10 text-[var(--ls-accent)]";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium capitalize",
        tone,
      )}
    >
      <span className="size-1.5 rounded-full bg-current" />
      {state.replaceAll("_", " ")}
    </span>
  );
}

function callerLabel(subject: string) {
  const value = subject.replace(/^api-key:/, "");
  return value.length > 12 ? `key:${value.slice(0, 8)}…` : value || "unknown";
}
