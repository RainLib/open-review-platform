import Link from "next/link";
import {
  ArrowLeft,
  CircleAlert,
  ExternalLink,
  GitBranch,
  RadioTower,
  ShieldCheck,
  SlidersHorizontal,
} from "lucide-react";
import { notFound } from "next/navigation";

import { OperatePageHeader } from "@/components/console/operate-page-header";
import { InstallationDeactivation } from "@/components/console/installation-deactivation";
import { InstallationVerificationRetry } from "@/components/console/installation-verification-retry";
import { ProviderMark } from "@/components/providers/provider-icons";
import {
  getProviderInstallationData,
  getInstallationWebhookReceipts,
  type InstallationWebhookReceiptData,
  type ProviderInstallation,
} from "@/lib/control-api";
import { providerIssueTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";

export default async function ConnectionDetailPage({
  params,
}: {
  params: Promise<{ org: string; installationId: string }>;
}) {
  const { org, installationId } = await params;
  const data = await getProviderInstallationData(org, installationId);
  const installation = data.installation;
  if (!installation && data.source === "live") notFound();
  const webhookData = installation
    ? await getInstallationWebhookReceipts(org, installation.id)
    : undefined;

  return (
    <div className="space-y-7">
      <OperatePageHeader
        active="connections"
        description="Inspect the provider installation that authorizes this workspace. Credentials, private keys, and webhook secrets never enter this page."
        eyebrow="Source providers"
        org={org}
        source={data.source}
        title="Connection detail"
      />
      <Link
        className="luminous-focus inline-flex items-center gap-2 rounded-[9px] px-2 py-1.5 text-sm font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
        href={`/${encodeURIComponent(org)}/connect?tab=installations`}
      >
        <ArrowLeft className="size-4" />
        All connections
      </Link>
      {!installation ? (
        <Unavailable
          detail={data.detail ?? "The selected connection could not be loaded."}
        />
      ) : (
        <ConnectionDetail
          installation={installation}
          live={data.source === "live"}
          org={org}
          webhookData={webhookData}
        />
      )}
    </div>
  );
}

function ConnectionDetail({
  installation,
  live,
  org,
  webhookData,
}: {
  installation: ProviderInstallation;
  live: boolean;
  org: string;
  webhookData?: InstallationWebhookReceiptData;
}) {
  const provider = installation.provider === "github" ? "GitHub" : "GitLab";
  const host = hostName(installation.api_base_url);
  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_330px]">
      <section className="overflow-hidden rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="border-b border-[var(--ls-line)] p-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex min-w-0 items-center gap-4">
              <span className="grid size-12 place-items-center rounded-[14px] bg-[var(--ls-surface-muted)]">
                <ProviderMark
                  className="size-6"
                  provider={installation.provider}
                />
              </span>
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <h2 className="text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">
                    {provider} installation
                  </h2>
                  <Status
                    active={installation.active}
                    verificationState={installation.verification_state}
                  />
                  <ProviderHealthStatus
                    healthState={installation.verification?.health_state}
                  />
                </div>
                <p className="mt-1 font-mono text-xs text-[var(--ls-text-tertiary)]">
                  {installation.id}
                </p>
              </div>
            </div>
            <a
              className="luminous-focus inline-flex items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] px-3 py-2 text-xs font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"
              href={installation.api_base_url}
              rel="noreferrer"
              target="_blank"
            >
              {host}
              <ExternalLink className="size-3.5" />
            </a>
          </div>
        </div>
        <dl className="divide-y divide-[var(--ls-line)]">
          <Metric
            icon={GitBranch}
            label="Authorized repository scope"
            mono
            value={installation.repository_scope}
          />
          <Metric
            label="Provider authorization"
            value="Held by the control plane"
          />
          <Metric
            label="Review admission"
            value={admissionLabel(installation)}
          />
          <Metric
            icon={RadioTower}
            label="Current provider access"
            value={providerHealthLabel(installation)}
          />
          <Metric
            icon={SlidersHorizontal}
            label="Review coverage"
            value={
              installation.automatic_reviews
                ? installation.author_scope === "mine"
                  ? "Automatically review only the OAuth-bound author's PRs"
                  : "Automatically review all authors' PRs in scope"
                : "On demand only"
            }
          />
          <Metric
            label="User Issue AI analysis"
            value={issueTriageLabel(installation)}
          />
          <Metric
            label="Minimum finding severity"
            value={capitalize(installation.minimum_severity)}
          />
          <Metric
            label="Provider API host"
            mono
            value={installation.api_base_url}
          />
        </dl>
      </section>
      <aside className="space-y-4 xl:sticky xl:top-20 xl:self-start">
        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
            <ShieldCheck className="size-4 text-[var(--ls-accent)]" />
            Credential boundary
          </div>
          <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
            This record proves the configured provider identity and repository
            scope. It does not include an OAuth token, App private key, callback
            secret, or provider permission grant.
          </p>
        </section>
        <section className="rounded-[18px] border border-amber-500/25 bg-amber-500/[0.06] p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-warning-text)]">
            <RadioTower className="size-4" />
            Webhook evidence
          </div>
          <WebhookEvidence data={webhookData} installation={installation} org={org} />
          <Link
            className="luminous-focus mt-3 inline-flex text-xs font-medium text-[var(--ls-accent)] hover:underline"
            href={`/${encodeURIComponent(org)}/connect?tab=webhooks&installation_id=${encodeURIComponent(installation.id)}`}
          >
            Open delivery evidence
          </Link>
          {live ? (
            <InstallationVerificationRetry
              active={installation.active}
              healthState={installation.verification?.health_state}
              installationID={installation.id}
              org={org}
              verificationState={installation.verification_state}
            />
          ) : null}
        </section>
        {live ? (
          <InstallationDeactivation
            active={installation.active}
            enabled
            installationID={installation.id}
            org={org}
            provider={installation.provider}
            repositoryScope={installation.repository_scope}
          />
        ) : (
          <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-5">
            <p className="text-sm font-semibold text-[var(--ls-text)]">
              Read-only preview
            </p>
            <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
              Verification retries and connection deactivation are available only for a live control-plane installation. Preview data cannot change provider admission.
            </p>
          </section>
        )}
      </aside>
    </div>
  );
}

function WebhookEvidence({
  data,
  installation,
  org,
}: {
  data?: InstallationWebhookReceiptData;
  installation: ProviderInstallation;
  org: string;
}) {
  if (!data || data.source === "unavailable" || data.source === "unconfigured") {
    return (
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        {data?.detail ?? "Accepted webhook evidence could not be loaded for this connection."}
      </p>
    );
  }
  if (data.receipts.length === 0) {
    return (
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        No callback has been admitted for this connection yet. A verified installation is not presented as delivery proof.
      </p>
    );
  }
  const latest = data.receipts[0];
  const issueTarget =
    latest.resource_kind === "issue"
      ? providerIssueTarget({
          provider: installation.provider,
          api_base_url: installation.api_base_url,
          repository: latest.repository,
          issue_number: latest.review_number,
        })
      : undefined;
  return (
    <div className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
      <p>
        Showing the {data.receipts.length} most recent accepted{" "}
        {data.receipts.length === 1 ? "callback" : "callbacks"}. Latest:{" "}
        <span className="font-mono text-[var(--ls-text)]">
          {latest.event_name}
        </span>{" "}
        on {latest.repository}{" "}
        {latest.resource_kind === "issue"
          ? "Issue"
          : installation.provider === "gitlab"
            ? "MR"
            : "PR"}{" "}
        #{latest.review_number}
        {latest.action ? ` · ${latest.action}` : ""}
        {latest.revision ? ` · r${latest.revision}` : ""}.
      </p>
      {latest.run_id ? (
        <Link
          className="mt-2 inline-flex font-medium text-[var(--ls-accent)] hover:underline"
          href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(latest.run_id)}`}
        >
          Open latest review evidence
        </Link>
      ) : issueTarget ? (
        <a
          className="mt-2 inline-flex items-center gap-1 font-medium text-[var(--ls-accent)] hover:underline"
          href={issueTarget.url}
          rel="noreferrer"
          target="_blank"
        >
          Open provider Issue
          <ExternalLink className="size-3" />
        </a>
      ) : null}
    </div>
  );
}

function Metric({
  icon: Icon,
  label,
  mono,
  value,
}: {
  icon?: typeof GitBranch;
  label: string;
  mono?: boolean;
  value: string;
}) {
  return (
    <div className="grid gap-1 px-6 py-4 sm:grid-cols-[220px_minmax(0,1fr)] sm:items-center">
      <dt className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]">
        {Icon ? <Icon className="size-3.5 text-[var(--ls-accent)]" /> : null}
        {label}
      </dt>
      <dd
        className={cn(
          "min-w-0 break-all text-sm text-[var(--ls-text)]",
          mono && "font-mono text-xs",
        )}
      >
        {value}
      </dd>
    </div>
  );
}
function Status({
  active,
  verificationState,
}: {
  active: boolean;
  verificationState: ProviderInstallation["verification_state"];
}) {
  const eligible =
    active &&
    (verificationState === "legacy" || verificationState === "verified");
  return (
    <span
      className={cn(
        "rounded-full px-2.5 py-1 text-xs font-medium",
        eligible
          ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
          : active
            ? "bg-amber-500/10 text-[var(--ls-warning-text)]"
            : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-tertiary)]",
      )}
    >
      {eligible
        ? verificationState === "legacy"
          ? "Legacy eligible"
          : "Verified"
        : active
          ? verificationState
          : "Inactive"}
    </span>
  );
}
function ProviderHealthStatus({
  healthState,
}: {
  healthState?: NonNullable<ProviderInstallation["verification"]>["health_state"];
}) {
  if (!healthState) return null;
  const healthy = healthState === "live";
  return (
    <span
      className={cn(
        "rounded-full px-2.5 py-1 text-xs font-medium",
        healthy
          ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
          : "bg-amber-500/10 text-[var(--ls-warning-text)]",
      )}
    >
      {healthy ? "Provider access live" : `Provider access ${healthState}`}
    </span>
  );
}
function admissionLabel(installation: ProviderInstallation) {
  if (!installation.active) return "Disabled";
  return installation.verification_state === "legacy" ||
    installation.verification_state === "verified"
    ? "Webhook and CLI admission enabled"
    : "Blocked until provider verification succeeds";
}
function providerHealthLabel(installation: ProviderInstallation) {
  const verification = installation.verification;
  if (!verification) return "No provider health receipt has been recorded";
  const observed = verification.observed_at
    ? ` · observed ${new Intl.DateTimeFormat("en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(verification.observed_at))}`
    : "";
  const error = verification.error_code
    ? ` · ${verification.error_code}`
    : "";
  return `${capitalize(verification.health_state)}${error}${observed}`;
}
function issueTriageLabel(installation: ProviderInstallation) {
  if (installation.provider === "gitlab") {
    return "Requires observed GitLab Issue Hook delivery";
  }
  const capability = installation.verification?.permissions.find((permission) =>
    permission.startsWith("issue_triage:"),
  );
  switch (capability) {
    case "issue_triage:ready":
      return "Ready · Issues event and write permission observed";
    case "issue_triage:missing_event":
      return "Action required · subscribe the GitHub App to Issues events";
    case "issue_triage:missing_write_permission":
      return "Action required · grant GitHub Issues write permission";
    case "issue_triage:unobserved":
      return "App registration could not be observed";
    default:
      return "Not observed by the latest provider probe";
  }
}
function Unavailable({ detail }: { detail: string }) {
  return (
    <section className="grid min-h-72 place-items-center rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-8 text-center">
      <div>
        <CircleAlert className="mx-auto size-7 text-[var(--ls-warning-text)]" />
        <h2 className="mt-4 text-lg font-semibold text-[var(--ls-text)]">
          Connection data unavailable
        </h2>
        <p className="mt-2 max-w-md text-sm text-[var(--ls-text-secondary)]">
          {detail}
        </p>
      </div>
    </section>
  );
}
function capitalize(value: string) {
  return value.slice(0, 1).toUpperCase() + value.slice(1);
}
function hostName(value: string) {
  try {
    return new URL(value).host;
  } catch {
    return value;
  }
}
