import Link from "next/link";
import {
  ExternalLink,
  GitBranch,
  RadioTower,
  ShieldCheck,
} from "lucide-react";

import { ConnectionManager } from "@/components/console/connection-manager";
import { InstallationRepositoryScopeEditor } from "@/components/console/installation-repository-scope-editor";
import { OperatePageHeader } from "@/components/console/operate-page-header";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { ProviderMark } from "@/components/providers/provider-icons";
import { getGitLabOAuthConfiguration } from "@/lib/auth/gitlab-oauth";
import { safeInternalPath } from "@/lib/auth/session";
import {
  getAuditData,
  getConsoleData,
  getInstallationRepositories,
  getInstallationWebhookReceipts,
  getProviderProfiles,
  type InstallationRepositoryData,
  type InstallationWebhookReceiptData,
  type ProviderInstallation,
} from "@/lib/control-api";
import { providerIssueTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

type ConnectionTab =
  | "installations"
  | "repositories"
  | "webhooks"
  | "activity";

const dateFormatter = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

export default async function ConnectPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{
    installation_id?: string | string[];
    return_to?: string | string[];
    tab?: string;
  }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const [data, providerProfiles] = await Promise.all([
    getConsoleData(org),
    getProviderProfiles(org),
  ]);
  const tab: ConnectionTab =
    query.tab === "repositories" ||
    query.tab === "webhooks" ||
    query.tab === "activity"
      ? query.tab
      : "installations";
  const returnTo = setupReturnTo(query.return_to, org);
  const selectedInstallation =
    (typeof query.installation_id === "string"
      ? data.installations.find(
          (installation) => installation.id === query.installation_id,
        )
      : undefined) ?? data.installations[0];
  const [webhookData, activityData, repositoryData] = await Promise.all([
    tab === "webhooks" && selectedInstallation
      ? getInstallationWebhookReceipts(org, selectedInstallation.id)
      : undefined,
    tab === "activity"
      ? getAuditData(org, { action: "installation." })
      : undefined,
    tab === "repositories" && selectedInstallation
      ? getInstallationRepositories(org, selectedInstallation.id)
      : undefined,
  ]);
  return (
    <div className="space-y-7">
      <OperatePageHeader
        active="connections"
        description="Connect GitHub or GitLab without exposing provider credentials to the browser. Installation identity, repository scope, and webhook evidence remain separate."
        eyebrow="Source providers"
        org={org}
        detail={data.detail}
        source={data.source}
        title="Connections"
      />
      <TabStateRouter
        className="inline-flex rounded-[11px] bg-[var(--ls-surface-muted)] p-1"
        label="Connection views"
      >
        {(["installations", "repositories", "webhooks", "activity"] as ConnectionTab[]).map(
          (item) => (
            <Link
              aria-current={item === tab ? "page" : undefined}
              aria-selected={item === tab}
              className={cn(
                "luminous-focus rounded-[8px] px-3 py-2 text-xs font-medium capitalize",
                item === tab
                  ? "bg-[var(--ls-surface)] text-[var(--ls-text)] shadow-[var(--ls-shadow-control)]"
                  : "text-[var(--ls-text-secondary)] hover:text-[var(--ls-text)]",
              )}
              href={connectionTabHref(org, item, selectedInstallation?.id, returnTo)}
              key={item}
              role="tab"
              tabIndex={item === tab ? 0 : -1}
            >
              {item}
            </Link>
          ),
        )}
      </TabStateRouter>
      {tab === "installations" ? (
        <>
          <div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
            <ShieldCheck className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
            The browser chooses a provider and repository scope only. GitHub App
            installation identity or GitLab authorization comes from the signed
            server-side handoff; private keys, tokens, callbacks, and webhook
            secrets stay in deployment custody.
          </div>
          <ConnectionManager
            detail={data.detail}
            gitlabDeploymentTokenAvailable={providerProfiles.profiles.some(
              (profile) =>
                profile.provider === "gitlab" &&
                profile.deployment_token_available,
            )}
            gitlabOAuthAvailable={Boolean(getGitLabOAuthConfiguration())}
            gitlabProfile={providerProfiles.profiles.find(
              (profile) => profile.provider === "gitlab",
            )}
            gitlabProfileSource={providerProfiles.source}
            installations={data.installations}
            org={org}
            source={data.source}
          />
        </>
      ) : tab === "repositories" ? (
        <RepositoryInventory
          data={repositoryData}
          installations={data.installations}
          org={org}
          returnTo={returnTo}
          selectedInstallation={selectedInstallation}
        />
      ) : tab === "webhooks" ? (
        <WebhookReceipts
          data={webhookData}
          installations={data.installations}
          org={org}
          selectedInstallation={selectedInstallation}
          selectedInstallationID={selectedInstallation?.id}
        />
      ) : (
        <ConnectionActivity data={activityData} org={org} />
      )}
    </div>
  );
}

function RepositoryInventory({
  data,
  installations,
  org,
  returnTo,
  selectedInstallation,
}: {
  data?: InstallationRepositoryData;
  installations: ProviderInstallation[];
  org: string;
  returnTo?: string;
  selectedInstallation?: ProviderInstallation;
}) {
  if (!selectedInstallation) {
    return (
      <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=installations`} variant="primary">View connections</RecoveryAction>} detail="Install and verify a provider connection before repository inventory can be synchronized." kind="first-use-empty" title="No repository authorization" />
    );
  }
  return (
    <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="border-b border-[var(--ls-line)] p-5">
        <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
          <div className="flex items-center gap-3">
            <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <GitBranch className="size-4" />
            </span>
            <div>
              <h2 className="font-semibold text-[var(--ls-text)]">Repository inventory</h2>
              <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">
                Read-only metadata synchronized by the provider worker and filtered by this connection’s recorded scope.
              </p>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {returnTo ? (
              <Link
                className="luminous-focus inline-flex h-9 items-center rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white hover:bg-[var(--ls-accent-hover)]"
                href={returnTo}
              >
                Resume setup
              </Link>
            ) : null}
            <Link
              className="luminous-focus inline-flex h-9 items-center rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"
              href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(selectedInstallation.id)}`}
            >
              Connection detail
            </Link>
          </div>
        </div>
        {installations.length > 1 ? (
          <nav aria-label="Choose connection inventory" className="mt-5 flex gap-2 overflow-x-auto pb-1">
            {installations.map((installation) => (
              <Link
                aria-current={installation.id === selectedInstallation.id ? "page" : undefined}
                className={cn(
                  "luminous-focus inline-flex shrink-0 items-center gap-2 rounded-[9px] border px-3 py-2 text-xs font-medium",
                  installation.id === selectedInstallation.id
                    ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                    : "border-[var(--ls-line)] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]",
                )}
                href={`/${encodeURIComponent(org)}/connect?tab=repositories&installation_id=${encodeURIComponent(installation.id)}`}
                key={installation.id}
              >
                <ProviderMark className="size-3.5" provider={installation.provider} />
                {installation.repository_scope}
              </Link>
            ))}
          </nav>
        ) : null}
      </div>
      {selectedInstallation.verification?.inventory_state === "partial" ? (
        <p className="border-b border-amber-500/20 bg-amber-500/[0.07] px-5 py-3 text-xs leading-5 text-[var(--ls-warning-text)]">
          This is a partial provider inventory. A later page failed or the bounded sync reached its limit; repositories absent from this list have not been checked. Enter an exact repository scope and retry verification if needed.
        </p>
      ) : null}
      {data?.source === "live" || (data?.source === "demo" && data.repositories.length > 0) ? (
        <div className="divide-y divide-[var(--ls-line)]">
          {data.source === "live" ? (
            <InstallationRepositoryScopeEditor
              active={selectedInstallation.active}
              currentScope={selectedInstallation.repository_scope}
              installationID={selectedInstallation.id}
              key={`${selectedInstallation.id}:${selectedInstallation.repository_scope}`}
              org={org}
              repositories={data.repositories}
              returnTo={returnTo}
            />
          ) : (
            <div className="flex flex-col gap-2 bg-[var(--ls-surface-muted)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p className="text-sm font-medium text-[var(--ls-text)]">
                  Read-only preview inventory
                </p>
                <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
                  Repository scope is deployment-controlled until a live provider worker verifies and synchronizes this connection.
                </p>
              </div>
              <span className="w-fit rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 text-xs font-medium text-[var(--ls-accent)]">
                No changes in preview
              </span>
            </div>
          )}
          {data.repositories.map((repository) => (
            <div className="flex flex-col justify-between gap-2 px-5 py-4 sm:flex-row sm:items-center" key={repository.external_id}>
              <div className="min-w-0">
                <p className="truncate font-mono text-sm font-medium text-[var(--ls-text)]">{repository.name}</p>
                <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
                  {repository.default_branch ? `Default branch: ${repository.default_branch}` : "Default branch not returned"}
                  {repository.visibility ? ` · ${repository.visibility}` : ""}
                </p>
              </div>
              <div className="flex items-center gap-2 text-xs">
                {repository.archived ? <span className="rounded-full bg-amber-500/10 px-2 py-1 text-[var(--ls-warning-text)]">Archived</span> : null}
                <time className="text-[var(--ls-text-tertiary)]" dateTime={repository.last_seen_at}>
                  Seen {dateFormatter.format(new Date(repository.last_seen_at))}
                </time>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(selectedInstallation.id)}`}>View connection evidence</RecoveryAction>} detail={data?.detail ?? "The connection must complete a worker-side read-only provider probe before repository metadata appears here."} kind={data?.source === "demo" ? "first-use-empty" : "unavailable"} title={data?.source === "demo" ? "Inventory is not available yet" : "Repository inventory is unavailable"} />
      )}
    </section>
  );
}

function setupReturnTo(value: string | string[] | undefined, org: string) {
  if (typeof value !== "string") return undefined;
  const candidate = safeInternalPath(value);
  if (candidate === "/workspaces") return undefined;
  const parsed = new URL(candidate, "https://openreview.invalid");
  return parsed.pathname === "/setup" && parsed.searchParams.get("tenant") === org
    ? candidate
    : undefined;
}

function connectionTabHref(
  org: string,
  tab: ConnectionTab,
  installationID: string | undefined,
  returnTo: string | undefined,
) {
  const query = new URLSearchParams({ tab });
  if (installationID) query.set("installation_id", installationID);
  if (returnTo) query.set("return_to", returnTo);
  return `/${encodeURIComponent(org)}/connect?${query.toString()}`;
}

function ConnectionActivity({
  data,
  org,
}: {
  data?: Awaited<ReturnType<typeof getAuditData>>;
  org: string;
}) {
  if (!data || (data.source !== "live" && data.source !== "demo")) {
    return <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=activity`}>Refresh activity</RecoveryAction>} detail={data?.detail ?? "The control plane did not return the immutable installation audit stream."} kind="unavailable" title="Connection activity is unavailable" />;
  }

  return (
    <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="flex flex-col justify-between gap-3 border-b border-[var(--ls-line)] px-5 py-4 sm:flex-row sm:items-center">
        <div>
          <h2 className="text-sm font-semibold text-[var(--ls-text)]">
            Connection activity
          </h2>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
            Append-only installation and verification events. Credentials,
            provider payloads, and callback secrets are excluded.
          </p>
        </div>
        <span className="w-fit rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">
          {data.events.length} events · UTC{data.source === "demo" ? " · preview" : ""}
        </span>
      </div>
      {data.events.length ? (
        <div className="divide-y divide-[var(--ls-line)]">
          {data.events.map((event) => (
            <Link
              className="group grid gap-2 px-5 py-4 transition hover:bg-[var(--ls-surface-muted)] sm:grid-cols-[168px_minmax(0,1fr)_minmax(0,1fr)] sm:items-center sm:gap-4"
              href={`/${encodeURIComponent(org)}/audit?tab=events&event=${encodeURIComponent(event.id)}&action=installation.`}
              key={event.id}
            >
              <time
                className="text-xs tabular-nums text-[var(--ls-text-tertiary)]"
                dateTime={event.created_at}
              >
                {dateFormatter.format(new Date(event.created_at))}
              </time>
              <span className="truncate font-mono text-xs font-medium text-[var(--ls-accent)]">
                {event.action}
              </span>
              <span className="truncate text-sm text-[var(--ls-text-secondary)] group-hover:text-[var(--ls-text)]">
                {event.target}
              </span>
            </Link>
          ))}
        </div>
      ) : (
        <PageState detail="Starting an installation or requesting verification records the first durable activity here." kind="first-use-empty" title="No connection events yet" />
      )}
    </section>
  );
}

function WebhookReceipts({
  data,
  installations,
  org,
  selectedInstallation,
  selectedInstallationID,
}: {
  data?: InstallationWebhookReceiptData;
  installations: Awaited<ReturnType<typeof getConsoleData>>["installations"];
  org: string;
  selectedInstallation?: ProviderInstallation;
  selectedInstallationID?: string;
}) {
  if (!installations.length) {
    return (
      <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=installations`} variant="primary">Connect provider</RecoveryAction>} detail="Install a provider connection before incoming review deliveries can be associated with a workspace." kind="first-use-empty" title="No provider connection" />
    );
  }
  return (
    <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="border-b border-[var(--ls-line)] p-5">
        <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
          <div className="flex items-center gap-3">
            <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <RadioTower className="size-4" />
            </span>
            <div>
              <h2 className="font-semibold text-[var(--ls-text)]">
                Accepted webhook deliveries
              </h2>
              <p className="mt-1 max-w-2xl text-sm leading-5 text-[var(--ls-text-secondary)]">
                Durable callbacks admitted into a review or Issue-analysis job. Raw
                payloads, signatures, and provider delivery identifiers are
                intentionally excluded.
              </p>
            </div>
          </div>
          <Link
            className="luminous-focus w-fit text-xs font-medium text-[var(--ls-accent)] hover:underline"
            href={`/${encodeURIComponent(org)}/settings/health?tab=providers`}
          >
            Provider health
          </Link>
        </div>
        <div
          className="mt-4 flex flex-wrap gap-2"
          role="list"
          aria-label="Provider installation"
        >
          {installations.map((installation) => {
            const selected = installation.id === selectedInstallationID;
            return (
              <Link
                aria-current={selected ? "page" : undefined}
                className={cn(
                  "luminous-focus inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs transition",
                  selected
                    ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-text)]"
                    : "border-[var(--ls-line)] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]",
                )}
                href={`/${encodeURIComponent(org)}/connect?tab=webhooks&installation_id=${encodeURIComponent(installation.id)}`}
                key={installation.id}
                role="listitem"
              >
                <ProviderMark
                  className="size-3.5"
                  provider={installation.provider}
                />
                {installation.repository_scope}
              </Link>
            );
          })}
        </div>
      </div>
      {!data ||
      data.source === "unavailable" ||
      data.source === "unconfigured" ? (
        <PageState action={<RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=webhooks&installation_id=${encodeURIComponent(selectedInstallationID ?? "")}`}>Refresh evidence</RecoveryAction>} detail={data?.detail ?? "The selected connection could not be resolved for webhook evidence."} kind="unavailable" title="Delivery evidence unavailable" />
      ) : data.receipts.length === 0 ? (
        <PageState detail={data.source === "demo" ? "Demo mode does not synthesize callbacks." : "No callback has been admitted for this connection yet. A verified installation is not presented as delivery proof."} kind="first-use-empty" title="No accepted deliveries yet" />
      ) : (
        <div className="divide-y divide-[var(--ls-line)]">
          {data.receipts.map((receipt) => {
            const issueTarget =
              receipt.resource_kind === "issue" && selectedInstallation
                ? providerIssueTarget({
                    provider: selectedInstallation.provider,
                    api_base_url: selectedInstallation.api_base_url,
                    repository: receipt.repository,
                    issue_number: receipt.review_number,
                  })
                : undefined;
            return (
              <article
                className="grid gap-3 p-5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center"
                key={receipt.id}
              >
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="font-mono text-xs font-medium text-[var(--ls-text)]">
                      {receipt.event_name}
                    </p>
                    <StatePill value={receipt.job_state} />
                    {receipt.run_state ? (
                      <StatePill value={receipt.run_state} />
                    ) : null}
                  </div>
                  <p className="mt-2 truncate text-sm text-[var(--ls-text)]">
                    {receipt.repository}{" "}
                    {receipt.resource_kind === "issue"
                      ? "Issue"
                      : selectedInstallation?.provider === "gitlab"
                        ? "MR"
                        : "PR"}{" "}
                    #{receipt.review_number}
                  </p>
                  <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
                    Received {formatReceiptTime(receipt.received_at)}
                    {receipt.trigger_kind ? ` · ${receipt.trigger_kind}` : ""}
                    {receipt.action ? ` · ${receipt.action}` : ""}
                    {receipt.revision ? ` · r${receipt.revision}` : ""}
                  </p>
                </div>
                {receipt.run_id ? (
                  <Link
                    className="luminous-focus inline-flex w-fit items-center rounded-[9px] border border-[var(--ls-line-strong)] px-3 py-2 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"
                    href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(receipt.run_id)}`}
                  >
                    Open review evidence
                  </Link>
                ) : issueTarget ? (
                  <a
                    className="luminous-focus inline-flex w-fit items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-3 py-2 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"
                    href={issueTarget.url}
                    rel="noreferrer"
                    target="_blank"
                  >
                    Open provider Issue
                    <ExternalLink className="size-3.5" />
                  </a>
                ) : (
                  <span className="text-xs text-[var(--ls-text-tertiary)]">
                    {receipt.resource_kind === "issue"
                      ? "Issue analysis receipt"
                      : "Run not created"}
                  </span>
                )}
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

function StatePill({ value }: { value: string }) {
  const tone =
    value === "completed" || value === "succeeded"
      ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
      : value === "failed" || value === "cancelled"
        ? "bg-rose-500/10 text-[var(--ls-danger-text)]"
        : "bg-amber-500/10 text-[var(--ls-warning-text)]";
  return (
    <span
      className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", tone)}
    >
      {value}
    </span>
  );
}

function formatReceiptTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat("en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}
