"use client";

import { FormEvent, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ArchiveRestore,
  ArrowRight,
  Check,
  CircleAlert,
  Clock3,
  CloudCog,
  Database,
  FileArchive,
  Download,
  Fingerprint,
  Globe2,
  HardDrive,
  LoaderCircle,
  LockKeyhole,
  MapPin,
  PauseCircle,
  PlayCircle,
  Plus,
  RefreshCw,
  Scale,
  ShieldAlert,
  ShieldCheck,
  Trash2,
} from "lucide-react";

import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { DataFreshness, PageState } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import type {
  DataBoundary,
  DataClass,
  DataGovernanceData,
  DataGovernanceJob,
  DataLegalHold,
  RetentionPolicy,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";

type DataGovernanceTab = "residency" | "retention" | "jobs";
type PendingAction = string | undefined;

const dataClassLabels: Record<DataClass, string> = {
  raw_webhook: "Raw webhook payloads",
  findings: "Review findings",
  audit: "Audit evidence",
  usage: "Usage ledger",
  operational_logs: "Operational logs",
};

const jobStateLabels: Record<DataGovernanceJob["state"], string> = {
  requested: "Requested",
  awaiting_approval: "Awaiting approval",
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
  rejected: "Rejected",
};

export function DataGovernanceManager({
  data,
  org,
  tab,
}: {
  data: DataGovernanceData;
  org: string;
  tab: DataGovernanceTab;
}) {
  const router = useRouter();
  const [pending, setPending] = useState<PendingAction>();
  const [notice, setNotice] = useState<{
    tone: "success" | "error";
    text: string;
  }>();
	const preview = data.source === "demo";

  async function mutate(
    path: string,
    action: string,
    body: unknown,
    success: string,
  ) {
	if (data.source !== "live") {
		setNotice({ tone: "error", text: "Preview data is read-only. Connect a live control plane before changing governance policy or creating a job." });
		return false;
	}
    setPending(action);
    setNotice(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/data-governance${path}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      const result = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok)
        throw new Error(result.error ?? `Request failed (${response.status}).`);
      setNotice({ tone: "success", text: success });
      router.refresh();
      return true;
    } catch (error) {
      setNotice({
        tone: "error",
        text:
          error instanceof Error
            ? error.message
            : "The data governance request could not be completed.",
      });
      return false;
    } finally {
      setPending(undefined);
    }
  }

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">
            Enterprise data controls
          </p>
          <h1 className="mt-2 text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">
            Data governance
          </h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Trace where workspace data crosses boundaries, govern retention with
            impact evidence, and run exports or destructive operations through
            durable approval-aware jobs.
          </p>
        </div>
        <DataFreshness detail={data.detail} state={data.source} />
      </header>

      <EnterpriseSettingsTabs active="data" org={org} />
      <TabStateRouter
        label="Data governance views"
        className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]"
      >
        <Subtab
          href={`/${org}/settings/data?tab=residency`}
          label="Residency & boundaries"
          selected={tab === "residency"}
        />
        <Subtab
          href={`/${org}/settings/data?tab=retention`}
          label="Retention & legal hold"
          selected={tab === "retention"}
        />
        <Subtab
          href={`/${org}/settings/data?tab=jobs`}
          label="Export & deletion jobs"
          selected={tab === "jobs"}
        />
      </TabStateRouter>

      {notice ? <Notice {...notice} /> : null}
      {data.source !== "live" && !preview ? (
        <Unavailable detail={data.detail} />
      ) : (
        <>
          {preview ? <section className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">Preview data is read-only. It illustrates governed workflows but cannot create, approve, release, or download anything.</section> : null}
          <fieldset className="contents" disabled={preview}>
            {tab === "retention" ? (
              <RetentionView
                data={data}
                mutate={mutate}
                org={org}
                pending={pending}
              />
            ) : tab === "jobs" ? (
              <JobsView data={data} mutate={mutate} org={org} pending={pending} />
            ) : (
              <ResidencyView
                data={data}
                mutate={mutate}
                org={org}
                pending={pending}
              />
            )}
          </fieldset>
        </>
      )}
    </div>
  );
}

function ResidencyView({ data, mutate, pending }: ViewProps) {
  const residency = data.residency;
  async function requestMigration(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const region = String(form.get("desired_region") ?? "").trim();
    await mutate(
      "/jobs",
      "migration",
      {
        kind: "region_migration",
        scope_kind: "tenant",
        desired_region: region,
        idempotency_key: `region-${region}-${Date.now()}`,
        reason: String(form.get("reason") ?? "").trim(),
      },
      "Region migration request created. A different owner must approve it before execution.",
    );
  }
  return (
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1.25fr)_minmax(300px,.75fr)]">
      <div className="space-y-5">
        <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">
                Authoritative declaration
              </p>
              <h2 className="mt-2 text-xl font-semibold text-[var(--ls-text)]">
                {residency.configured
                  ? residency.primary_region
                  : "Residency has not been declared"}
              </h2>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">
                {residency.configured
                  ? `Revision ${residency.revision} is effective. Observed infrastructure evidence remains distinct from configured intent.`
                  : "The control plane will not infer residency from a browser location or cloud hostname. Unknown boundaries stay visibly unknown until the deployment records and observes them."}
              </p>
            </div>
            <StatusPill tone={residency.configured ? "success" : "warning"}>
              {residency.configured
                ? `Configured · r${residency.revision}`
                : "Undeclared"}
            </StatusPill>
          </div>
          <div className="mt-7 grid gap-3 md:grid-cols-2">
            {data.boundaries.map((boundary) => (
              <BoundaryCard boundary={boundary} key={boundary.name} />
            ))}
          </div>
        </section>
        <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 sm:p-6">
          <div className="flex items-start gap-3">
            <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <MapPin className="size-5" />
            </span>
            <div>
              <h2 className="text-lg font-semibold text-[var(--ls-text)]">
                Request a region migration
              </h2>
              <p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">
                This creates an immutable request only. A non-requesting owner
                must approve it; an executor must then emit observed evidence
                before residency changes.
              </p>
            </div>
          </div>
          <form
            className="mt-5 grid gap-4 sm:grid-cols-2"
            onSubmit={requestMigration}
          >
            <Field
              label="Destination region"
              name="desired_region"
              placeholder="eu-west-1"
              required
            />
            <Field
              label="Change reason"
              name="reason"
              placeholder="Contracted residency requirement"
              required
            />
            <button
              className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45 sm:col-span-2 sm:justify-self-end"
              disabled={pending === "migration"}
              type="submit"
            >
              {pending === "migration" ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : (
                <ArrowRight className="size-4" />
              )}
              Request migration
            </button>
          </form>
        </section>
      </div>
      <aside className="space-y-5">
        <Invariant icon={Fingerprint} title="Configured is not observed">
          A declared region is intent. The observed timestamp and execution
          receipt are separate evidence and must come from trusted
          infrastructure.
        </Invariant>
        <Invariant icon={Globe2} title="Model boundary">
          {residency.model_boundary || "external/unknown"}. Provider prompts and
          model processing may cross the primary storage boundary unless the
          selected model route guarantees otherwise.
        </Invariant>
        <Invariant icon={ShieldCheck} title="Migration invariant">
          Repository content cannot self-authorize a region move. Approval and
          execution are control-plane operations bound to the requested
          revision.
        </Invariant>
      </aside>
    </div>
  );
}

function RetentionView({ data, mutate, pending }: ViewProps) {
  const [policyClass, setPolicyClass] = useState<DataClass>("findings");
  const [policyScope, setPolicyScope] = useState<"tenant" | "repository">(
    "tenant",
  );
  const activePolicy = useMemo(
    () =>
      data.policies.find(
        (item) =>
          item.state === "active" &&
          item.data_class === policyClass &&
          item.scope_kind === policyScope,
      ),
    [data.policies, policyClass, policyScope],
  );
  const pendingPolicies = data.policies.filter(
    (item) => item.state === "awaiting_approval",
  );

  async function createPolicy(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await mutate(
      "/retention-policies",
      "policy",
      {
        scope_kind: policyScope,
        scope_ref:
          policyScope === "repository"
            ? String(form.get("scope_ref") ?? "").trim()
            : "",
        data_class: policyClass,
        retention_days: Number(form.get("retention_days")),
        expected_revision: activePolicy?.revision ?? 0,
        change_reason: String(form.get("change_reason") ?? "").trim(),
      },
      activePolicy
        ? "Retention change recorded. A shorter period remains pending until another owner accepts the impact."
        : "Initial retention policy is active and audited.",
    );
  }
  async function createHold(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const scope = String(form.get("scope_kind")) as "tenant" | "repository";
    await mutate(
      "/legal-holds",
      "hold",
      {
        scope_kind: scope,
        scope_ref:
          scope === "repository"
            ? String(form.get("scope_ref") ?? "").trim()
            : "",
        data_class: String(form.get("data_class")),
        reason: String(form.get("reason") ?? "").trim(),
      },
      "Legal hold activated. Matching deletion and retention execution is now blocked.",
    );
  }
  return (
    <div className="space-y-5">
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1.15fr)_minmax(320px,.85fr)]">
        <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-accent)]">
                Policy revision
              </p>
              <h2 className="mt-2 text-xl font-semibold text-[var(--ls-text)]">
                Retention by data class
              </h2>
              <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
                Shortening an active period generates an impact preview and
                requires approval. Extending retention can activate immediately.
              </p>
            </div>
            <Clock3 className="size-5 text-[var(--ls-text-tertiary)]" />
          </div>
          <form
            className="mt-6 grid gap-4 sm:grid-cols-2"
            onSubmit={createPolicy}
          >
            <Select
              label="Scope"
              name="scope_kind"
              onChange={(value) =>
                setPolicyScope(value as "tenant" | "repository")
              }
              options={[
                ["tenant", "Workspace"],
                ["repository", "Repository"],
              ]}
              value={policyScope}
            />
            <Select
              label="Data class"
              name="data_class"
              onChange={(value) => setPolicyClass(value as DataClass)}
              options={Object.entries(dataClassLabels)}
              value={policyClass}
            />
            {policyScope === "repository" ? (
              <Field
                label="Repository"
                name="scope_ref"
                placeholder="owner/repository"
                required
              />
            ) : null}
            <Field
              defaultValue={String(activePolicy?.retention_days ?? 90)}
              label="Retention days"
              name="retention_days"
              required
              type="number"
            />
            <div
              className={cn(
                policyScope === "repository"
                  ? "sm:col-span-2"
                  : "sm:col-span-2",
              )}
            >
              <Field
                label="Change reason"
                name="change_reason"
                placeholder="Why this period and scope are required"
                required
              />
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 sm:col-span-2">
              <p className="text-xs text-[var(--ls-text-tertiary)]">
                Expected revision: {activePolicy?.revision ?? 0}
                {activePolicy
                  ? ` · currently ${activePolicy.retention_days} days`
                  : " · no active policy"}
              </p>
              <button
                className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45"
                disabled={pending === "policy"}
                type="submit"
              >
                {pending === "policy" ? (
                  <LoaderCircle className="size-4 animate-spin" />
                ) : (
                  <Plus className="size-4" />
                )}
                Propose policy
              </button>
            </div>
          </form>
        </section>
        <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 sm:p-7">
          <div className="flex items-start gap-3">
            <span className="grid size-10 place-items-center rounded-[12px] bg-red-500/[0.08] text-[var(--ls-critical-text)]">
              <LockKeyhole className="size-5" />
            </span>
            <div>
              <h2 className="text-lg font-semibold text-[var(--ls-text)]">
                Create legal hold
              </h2>
              <p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">
                Legal hold wins over retention and deletion, including jobs
                approved before the hold was added.
              </p>
            </div>
          </div>
          <form className="mt-5 grid gap-4" onSubmit={createHold}>
            <Select
              label="Scope"
              name="scope_kind"
              options={[
                ["tenant", "Workspace"],
                ["repository", "Repository"],
              ]}
            />
            <Field
              label="Repository (required for repository scope)"
              name="scope_ref"
              placeholder="owner/repository"
            />
            <Select
              label="Protected data"
              name="data_class"
              options={[
                ["all", "All data classes"],
                ...Object.entries(dataClassLabels),
              ]}
            />
            <Field
              label="Legal or incident reason"
              name="reason"
              placeholder="Preservation requirement and reference"
              required
            />
            <button
              className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-red-500/25 bg-red-500/[0.06] px-4 text-sm font-semibold text-[var(--ls-critical-text)] disabled:opacity-45"
              disabled={pending === "hold"}
              type="submit"
            >
              {pending === "hold" ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : (
                <ShieldAlert className="size-4" />
              )}
              Activate legal hold
            </button>
          </form>
        </section>
      </div>
      {pendingPolicies.length ? (
        <section className="rounded-[18px] border border-amber-500/20 bg-amber-500/[0.035] p-5 sm:p-6">
          <div className="flex items-center gap-2">
            <Scale className="size-4 text-[var(--ls-warning-text)]" />
            <h2 className="text-sm font-semibold text-[var(--ls-text)]">
              Retention approvals
            </h2>
            <StatusPill tone="warning">
              {pendingPolicies.length} pending
            </StatusPill>
          </div>
          <div className="mt-4 grid gap-3 lg:grid-cols-2">
            {pendingPolicies.map((policy) => (
              <RetentionApprovalCard
                key={policy.id}
                mutate={mutate}
                pending={pending}
                policy={policy}
              />
            ))}
          </div>
        </section>
      ) : null}
      <div className="grid gap-5 lg:grid-cols-2">
        <PolicyList policies={data.policies} />
        <LegalHoldList
          holds={data.legal_holds}
          mutate={mutate}
          pending={pending}
        />
      </div>
    </div>
  );
}

function JobsView({ data, mutate, org, pending }: ViewProps) {
  const [kind, setKind] = useState<"export" | "deletion">("export");
  const activeJobs = data.jobs.filter(
    (item) => !["completed", "cancelled", "rejected"].includes(item.state),
  );
  async function createJob(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const scope = String(form.get("scope_kind"));
    await mutate(
      "/jobs",
      "job",
      {
        kind,
        scope_kind: scope,
        scope_ref:
          scope === "tenant" ? "" : String(form.get("scope_ref") ?? "").trim(),
        data_classes: [String(form.get("data_class"))],
        idempotency_key: `${kind}-${crypto.randomUUID()}`,
        reason: String(form.get("reason") ?? "").trim(),
      },
      kind === "deletion"
        ? "Deletion request created. Another owner must approve it; the approved request retains a cancellation window."
        : "Export queued. An artifact link will appear only after a trusted executor completes it.",
    );
  }
  return (
    <div className="space-y-5">
      <section className="grid gap-5 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7 lg:grid-cols-[minmax(0,1fr)_minmax(300px,.75fr)]">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-accent)]">
            Durable operation
          </p>
          <h2 className="mt-2 text-xl font-semibold text-[var(--ls-text)]">
            Create export or deletion job
          </h2>
          <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
            Every request has an idempotency identity, actor, revision, state
            trail, and audit event. Destructive work cannot skip owner approval.
          </p>
          <form className="mt-6 grid gap-4 sm:grid-cols-2" onSubmit={createJob}>
            <Select
              label="Operation"
              name="kind"
              onChange={(value) => setKind(value as "export" | "deletion")}
              options={[
                ["export", "Export evidence"],
                ["deletion", "Delete governed data"],
              ]}
              value={kind}
            />
            <Select
              label="Scope"
              name="scope_kind"
              options={
                kind === "deletion"
                  ? [
                      ["tenant", "Workspace"],
                      ["repository", "Repository"],
                    ]
                  : [
                      ["tenant", "Workspace"],
                      ["repository", "Repository"],
                      ["review_run", "Review run"],
                      ["audit_range", "Audit range"],
                    ]
              }
            />
            <Field
              label="Scope reference (blank for workspace)"
              name="scope_ref"
              placeholder={
                kind === "deletion"
                  ? "owner/repository"
                  : "owner/repository, run UUID, or RFC3339/RFC3339"
              }
            />
            <Select
              label="Data class"
              name="data_class"
              options={
                kind === "deletion"
                  ? [
                      ["raw_webhook", dataClassLabels.raw_webhook],
                      ["findings", dataClassLabels.findings],
                      ["audit", dataClassLabels.audit],
                    ]
                  : Object.entries(dataClassLabels)
              }
            />
            <div className="sm:col-span-2">
              <Field
                label="Operational reason"
                name="reason"
                placeholder="Audit request, customer export, or approved erasure reason"
                required
              />
            </div>
            <button
              className={cn(
                "luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] px-4 text-sm font-semibold text-white disabled:opacity-45 sm:col-span-2 sm:justify-self-end",
                kind === "deletion" ? "bg-red-600" : "bg-[var(--ls-accent)]",
              )}
              disabled={pending === "job"}
              type="submit"
            >
              {pending === "job" ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : kind === "deletion" ? (
                <Trash2 className="size-4" />
              ) : (
                <FileArchive className="size-4" />
              )}
              {kind === "deletion" ? "Request deletion" : "Queue export"}
            </button>
          </form>
        </div>
        <div className="grid content-start gap-3">
          <JobMetric
            icon={PlayCircle}
            label="Active jobs"
            value={String(activeJobs.length)}
          />
          <JobMetric
            icon={Scale}
            label="Awaiting approval"
            value={String(
              data.jobs.filter((item) => item.state === "awaiting_approval")
                .length,
            )}
          />
          <JobMetric
            icon={FileArchive}
            label="Artifacts ready"
            value={String(
              data.jobs.filter((item) => item.artifact_ready).length,
            )}
          />
          <div className="rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
            <strong className="text-[var(--ls-text)]">
              Truthful artifact state:
            </strong>{" "}
            queued or running exports do not expose a download. Completion
            requires a trusted executor receipt and a ready artifact reference.
          </div>
        </div>
      </section>
      <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)]">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--ls-line)] px-5 py-4">
          <div>
            <h2 className="text-sm font-semibold text-[var(--ls-text)]">
              Operation history
            </h2>
            <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
              Newest requests first · revisions prevent stale actions
            </p>
          </div>
          <StatusPill tone="neutral">{data.jobs.length} jobs</StatusPill>
        </div>
        {data.jobs.length ? (
          <div className="divide-y divide-[var(--ls-line)]">
            {data.jobs.map((job) => (
              <JobRow
                job={job}
                key={job.id}
                mutate={mutate}
                org={org}
                pending={pending}
              />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={ArchiveRestore}
            title="No governed operations"
            detail="Exports, deletions, and region migrations will appear here with their approval and execution evidence."
          />
        )}
      </section>
    </div>
  );
}

type ViewProps = {
  data: DataGovernanceData;
  org: string;
  pending: PendingAction;
  mutate: (
    path: string,
    action: string,
    body: unknown,
    success: string,
  ) => Promise<boolean>;
};

function BoundaryCard({ boundary }: { boundary: DataBoundary }) {
  const Icon = boundary.external
    ? Globe2
    : boundary.name.toLowerCase().includes("queue")
      ? CloudCog
      : boundary.name.toLowerCase().includes("object")
        ? HardDrive
        : Database;
  return (
    <article className="rounded-[15px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
      <div className="flex items-start gap-3">
        <span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-surface)] text-[var(--ls-accent)]">
          <Icon className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold text-[var(--ls-text)]">
              {boundary.name}
            </h3>
            <StatusPill tone={boundary.observed ? "success" : "warning"}>
              {boundary.observed ? "Observed" : "Configured only"}
            </StatusPill>
          </div>
          <p className="mt-2 break-all font-mono text-xs text-[var(--ls-text)]">
            {boundary.value}
          </p>
          <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
            {boundary.detail}
          </p>
        </div>
      </div>
    </article>
  );
}

function RetentionApprovalCard({
  policy,
  mutate,
  pending,
}: { policy: RetentionPolicy } & Pick<ViewProps, "mutate" | "pending">) {
  async function decide(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const submitter = (event.nativeEvent as SubmitEvent)
      .submitter as HTMLButtonElement | null;
    const decision = submitter?.value ?? "rejected";
    await mutate(
      `/retention-policies/${encodeURIComponent(policy.id)}/decisions`,
      `policy:${policy.id}`,
      {
        decision,
        reason: String(form.get("reason") ?? "").trim(),
        expected_revision: policy.revision,
      },
      decision === "approved"
        ? "Retention policy approved and activated."
        : "Retention proposal rejected; the prior active policy remains unchanged.",
    );
  }
  return (
    <article className="rounded-[15px] border border-amber-500/20 bg-[var(--ls-surface)] p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-sm font-semibold text-[var(--ls-text)]">
            {dataClassLabels[policy.data_class]} · {policy.retention_days} days
          </p>
          <p className="mt-1 text-xs text-[var(--ls-text-secondary)]">
            {scopeLabel(policy.scope_kind, policy.scope_ref)} · r
            {policy.revision} · requested by {policy.requested_by}
          </p>
        </div>
        <StatusPill tone={policy.impact_legal_hold ? "critical" : "warning"}>
          {policy.impact_records.toLocaleString("en-US")} impacted
        </StatusPill>
      </div>
      <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
        {policy.change_reason}
      </p>
      {policy.impact_legal_hold ? (
        <p className="mt-2 text-xs font-medium text-[var(--ls-critical-text)]">
          An active legal hold intersects this impact set.
        </p>
      ) : null}
      <form className="mt-4 flex flex-col gap-2 sm:flex-row" onSubmit={decide}>
        <input
          className="luminous-focus h-9 min-w-0 flex-1 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs text-[var(--ls-text)]"
          name="reason"
          placeholder="Decision evidence"
          required
        />
        <button
          className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-medium text-[var(--ls-text)] disabled:opacity-45"
          disabled={pending === `policy:${policy.id}`}
          name="decision"
          type="submit"
          value="rejected"
        >
          Reject
        </button>
        <button
          className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white disabled:opacity-45"
          disabled={pending === `policy:${policy.id}`}
          name="decision"
          type="submit"
          value="approved"
        >
          {pending === `policy:${policy.id}` ? (
            <LoaderCircle className="size-3.5 animate-spin" />
          ) : (
            <Check className="size-3.5" />
          )}
          Approve
        </button>
      </form>
    </article>
  );
}

function PolicyList({ policies }: { policies: RetentionPolicy[] }) {
  const current = policies.filter((item) => item.state !== "awaiting_approval");
  return (
    <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 sm:p-6">
      <div className="flex items-center gap-2">
        <Clock3 className="size-4 text-[var(--ls-accent)]" />
        <h2 className="text-sm font-semibold text-[var(--ls-text)]">
          Policy history
        </h2>
      </div>
      <div className="mt-4 space-y-2">
        {current.length ? (
          current.map((policy) => (
            <article
              className="rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"
              key={policy.id}
            >
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-[var(--ls-text)]">
                  {dataClassLabels[policy.data_class]} · {policy.retention_days}{" "}
                  days
                </p>
                <StatusPill
                  tone={
                    policy.state === "active"
                      ? "success"
                      : policy.state === "rejected"
                        ? "critical"
                        : "neutral"
                  }
                >
                  {policy.state}
                </StatusPill>
              </div>
              <p className="mt-1 text-xs text-[var(--ls-text-secondary)]">
                {scopeLabel(policy.scope_kind, policy.scope_ref)} · r
                {policy.revision} · {policy.change_reason}
              </p>
            </article>
          ))
        ) : (
          <EmptyState
            icon={Clock3}
            title="No retention policies"
            detail="Create the first explicit period for each governed data class."
          />
        )}
      </div>
    </section>
  );
}

function LegalHoldList({
  holds,
  mutate,
  pending,
}: { holds: DataLegalHold[] } & Pick<ViewProps, "mutate" | "pending">) {
  return (
    <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 sm:p-6">
      <div className="flex items-center gap-2">
        <LockKeyhole className="size-4 text-[var(--ls-critical-text)]" />
        <h2 className="text-sm font-semibold text-[var(--ls-text)]">
          Legal holds
        </h2>
      </div>
      <div className="mt-4 space-y-2">
        {holds.length ? (
          holds.map((hold) => (
            <article
              className="rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"
              key={hold.id}
            >
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-[var(--ls-text)]">
                    {hold.data_class === "all"
                      ? "All data classes"
                      : dataClassLabels[hold.data_class]}
                  </p>
                  <p className="mt-1 text-xs text-[var(--ls-text-secondary)]">
                    {scopeLabel(hold.scope_kind, hold.scope_ref)} ·{" "}
                    {hold.reason}
                  </p>
                </div>
                {hold.state === "active" ? (
                  <button
                    className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] border border-[var(--ls-line-strong)] px-2.5 text-xs font-medium text-[var(--ls-text)] disabled:opacity-45"
                    disabled={pending === `hold:${hold.id}`}
                    onClick={() =>
                      mutate(
                        `/legal-holds/${encodeURIComponent(hold.id)}/release`,
                        `hold:${hold.id}`,
                        { expected_revision: hold.revision },
                        "Legal hold released. Future destructive requests will still require their own approval.",
                      )
                    }
                    type="button"
                  >
                    {pending === `hold:${hold.id}` ? (
                      <LoaderCircle className="size-3.5 animate-spin" />
                    ) : (
                      <PauseCircle className="size-3.5" />
                    )}
                    Release
                  </button>
                ) : (
                  <StatusPill tone="neutral">Released</StatusPill>
                )}
              </div>
            </article>
          ))
        ) : (
          <EmptyState
            icon={LockKeyhole}
            title="No legal holds"
            detail="Use a hold when preservation must override all deletion and retention work."
          />
        )}
      </div>
    </section>
  );
}

function JobRow({
  job,
  mutate,
  org,
  pending,
}: { job: DataGovernanceJob } & Pick<ViewProps, "mutate" | "org" | "pending">) {
  async function decide(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const submitter = (event.nativeEvent as SubmitEvent)
      .submitter as HTMLButtonElement | null;
    const decision = submitter?.value ?? "rejected";
    await mutate(
      `/jobs/${encodeURIComponent(job.id)}/decisions`,
      `job:${job.id}`,
      {
        decision,
        reason: String(form.get("reason") ?? "").trim(),
        expected_revision: job.revision,
      },
      decision === "approved"
        ? "Operation approved and queued. Execution evidence is still required."
        : "Operation rejected without executing data changes.",
    );
  }
  const cancellable = ["requested", "awaiting_approval", "queued"].includes(
    job.state,
  );
  return (
    <article className="p-5">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className={cn(
                "grid size-8 place-items-center rounded-[10px]",
                job.kind === "deletion"
                  ? "bg-red-500/[0.08] text-[var(--ls-critical-text)]"
                  : job.kind === "region_migration"
                    ? "bg-violet-500/[0.08] text-violet-500"
                    : "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]",
              )}
            >
              {job.kind === "deletion" ? (
                <Trash2 className="size-4" />
              ) : job.kind === "region_migration" ? (
                <MapPin className="size-4" />
              ) : (
                <FileArchive className="size-4" />
              )}
            </span>
            <h3 className="text-sm font-semibold capitalize text-[var(--ls-text)]">
              {job.kind.replaceAll("_", " ")}
            </h3>
            <JobStatePill state={job.state} />
            <span className="font-mono text-[10px] text-[var(--ls-text-tertiary)]">
              r{job.revision}
            </span>
          </div>
          <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
            {scopeLabel(job.scope_kind, job.scope_ref)} ·{" "}
            {job.data_classes.map((item) => dataClassLabels[item]).join(", ") ||
              job.desired_region}
          </p>
          <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
            {job.reason} · requested by {job.requested_by} ·{" "}
            {formatDate(job.created_at)}
          </p>
          {job.kind === "region_migration" && job.external_operation_id ? (
            <p className="mt-2 text-xs text-[var(--ls-text-secondary)]">
              Orchestrator operation{" "}
              <span className="font-mono">{job.external_operation_id}</span> ·{" "}
              {job.progress}%
              {job.next_attempt_at
                ? ` · next reconciliation ${formatDate(job.next_attempt_at)}`
                : ""}
            </p>
          ) : null}
          {job.error_message ? (
            <p className="mt-2 text-xs text-[var(--ls-critical-text)]">
              {job.error_code}: {job.error_message}
            </p>
          ) : null}
          {job.reversible_until ? (
            <p className="mt-2 inline-flex items-center gap-1.5 text-xs text-[var(--ls-warning-text)]">
              <Clock3 className="size-3.5" />
              Cancellation window until {formatDate(job.reversible_until)}
            </p>
          ) : null}
          {job.artifact_ready ? (
            <p className="mt-2 text-xs font-medium text-[var(--ls-success-text)]">
              Encrypted artifact ready. Download access is authenticated,
              audited, and expires automatically.
            </p>
          ) : job.kind === "export" &&
            ["queued", "running"].includes(job.state) ? (
            <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">
              No download yet — executor receipt is pending.
            </p>
          ) : null}
          {Object.keys(job.receipt).length ? (
            <details className="mt-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2">
              <summary className="luminous-focus cursor-pointer text-xs font-medium text-[var(--ls-text)]">
                Execution receipt
              </summary>
              <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all text-[10px] leading-5 text-[var(--ls-text-secondary)]">
                {JSON.stringify(job.receipt, null, 2)}
              </pre>
            </details>
          ) : null}
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {job.artifact_ready ? (
            <a
              className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] bg-[var(--ls-accent)] px-2.5 text-xs font-semibold text-white"
              href={`/api/tenants/${encodeURIComponent(org)}/data-governance/jobs/${encodeURIComponent(job.id)}/artifact`}
            >
              <Download className="size-3.5" />
              Download
            </a>
          ) : null}
          {cancellable ? (
            <button
              className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] border border-[var(--ls-line-strong)] px-2.5 text-xs font-medium text-[var(--ls-text)] disabled:opacity-45"
              disabled={pending === `cancel:${job.id}`}
              onClick={() =>
                mutate(
                  `/jobs/${encodeURIComponent(job.id)}/cancel`,
                  `cancel:${job.id}`,
                  { expected_revision: job.revision },
                  "Operation cancelled before its irreversible boundary.",
                )
              }
              type="button"
            >
              {pending === `cancel:${job.id}` ? (
                <LoaderCircle className="size-3.5 animate-spin" />
              ) : (
                <PauseCircle className="size-3.5" />
              )}
              Cancel
            </button>
          ) : null}
          {job.state === "failed" ? (
            <button
              className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[8px] border border-[var(--ls-line-strong)] px-2.5 text-xs font-medium text-[var(--ls-text)] disabled:opacity-45"
              disabled={pending === `retry:${job.id}`}
              onClick={() =>
                mutate(
                  `/jobs/${encodeURIComponent(job.id)}/retry`,
                  `retry:${job.id}`,
                  {
                    expected_revision: job.revision,
                    idempotency_key: `retry-${crypto.randomUUID()}`,
                    reason: "Operator retry after failure review",
                  },
                  "A linked retry job was created with a new idempotency identity.",
                )
              }
              type="button"
            >
              {pending === `retry:${job.id}` ? (
                <LoaderCircle className="size-3.5 animate-spin" />
              ) : (
                <RefreshCw className="size-3.5" />
              )}
              Retry
            </button>
          ) : null}
        </div>
      </div>
      {job.state === "awaiting_approval" ? (
        <form
          className="mt-4 flex flex-col gap-2 border-t border-[var(--ls-line)] pt-4 sm:flex-row"
          onSubmit={decide}
        >
          <input
            className="luminous-focus h-9 min-w-0 flex-1 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs text-[var(--ls-text)]"
            name="reason"
            placeholder="Independent approval evidence"
            required
          />
          <button
            className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] px-3 text-xs font-medium text-[var(--ls-text)]"
            name="decision"
            type="submit"
            value="rejected"
          >
            Reject
          </button>
          <button
            className="luminous-focus inline-flex h-9 items-center justify-center gap-1.5 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white disabled:opacity-45"
            disabled={pending === `job:${job.id}`}
            name="decision"
            type="submit"
            value="approved"
          >
            {pending === `job:${job.id}` ? (
              <LoaderCircle className="size-3.5 animate-spin" />
            ) : (
              <Check className="size-3.5" />
            )}
            Approve
          </button>
        </form>
      ) : null}
    </article>
  );
}

function JobMetric({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof PlayCircle;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
      <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)]">
        <Icon className="size-4" />
      </span>
      <div>
        <p className="text-xs text-[var(--ls-text-secondary)]">{label}</p>
        <p className="mt-0.5 text-lg font-semibold text-[var(--ls-text)]">
          {value}
        </p>
      </div>
    </div>
  );
}
function JobStatePill({ state }: { state: DataGovernanceJob["state"] }) {
  const tone =
    state === "completed"
      ? "success"
      : state === "failed" || state === "rejected"
        ? "critical"
        : state === "awaiting_approval" || state === "queued"
          ? "warning"
          : "neutral";
  return (
    <StatusPill tone={tone}>
      {state === "running" ? (
        <LoaderCircle className="size-3 animate-spin" />
      ) : null}
      {jobStateLabels[state]}
    </StatusPill>
  );
}
function StatusPill({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone: "success" | "warning" | "critical" | "neutral";
}) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-semibold",
        tone === "success"
          ? "bg-emerald-500/10 text-[var(--ls-success-text)]"
          : tone === "warning"
            ? "bg-amber-500/10 text-[var(--ls-warning-text)]"
            : tone === "critical"
              ? "bg-red-500/10 text-[var(--ls-critical-text)]"
              : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
      )}
    >
      {children}
    </span>
  );
}
function Invariant({
  icon: Icon,
  title,
  children,
}: {
  icon: typeof ShieldCheck;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5">
      <Icon className="size-4 text-[var(--ls-accent)]" />
      <h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">
        {title}
      </h3>
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        {children}
      </p>
    </section>
  );
}
function Field({
  defaultValue,
  label,
  name,
  placeholder,
  required,
  type = "text",
}: {
  defaultValue?: string;
  label: string;
  name: string;
  placeholder?: string;
  required?: boolean;
  type?: string;
}) {
  return (
    <label className="block">
      <span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">
        {label}
      </span>
      <input
        className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]"
        defaultValue={defaultValue}
        min={type === "number" ? 1 : undefined}
        name={name}
        placeholder={placeholder}
        required={required}
        type={type}
      />
    </label>
  );
}
function Select({
  label,
  name,
  onChange,
  options,
  value,
}: {
  label: string;
  name: string;
  onChange?: (value: string) => void;
  options: Array<[string, string]>;
  value?: string;
}) {
  return (
    <label className="block">
      <span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">
        {label}
      </span>
      <select
        className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]"
        name={name}
        onChange={
          onChange ? (event) => onChange(event.target.value) : undefined
        }
        value={value}
      >
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </select>
    </label>
  );
}
function Subtab({
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
        "luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 pt-3 text-sm font-medium",
        selected
          ? "text-[var(--ls-text)]"
          : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]",
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
function Notice({ text, tone }: { text: string; tone: "success" | "error" }) {
  return (
    <div
      aria-live="polite"
      className={cn(
        "flex items-start gap-2 rounded-[12px] border px-4 py-3 text-sm",
        tone === "success"
          ? "border-emerald-500/20 bg-emerald-500/[0.07] text-[var(--ls-success-text)]"
          : "border-red-500/20 bg-red-500/[0.07] text-[var(--ls-critical-text)]",
      )}
    >
      <CircleAlert className="mt-0.5 size-4 shrink-0" />
      {text}
    </div>
  );
}
function EmptyState({
  icon: Icon,
  title,
  detail,
}: {
  icon: typeof ArchiveRestore;
  title: string;
  detail: string;
}) {
  return (
    <div className="grid min-h-36 place-items-center p-8 text-center">
      <div>
        <Icon className="mx-auto size-5 text-[var(--ls-text-tertiary)]" />
        <h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">
          {title}
        </h3>
        <p className="mt-1 max-w-md text-xs leading-5 text-[var(--ls-text-secondary)]">
          {detail}
        </p>
      </div>
    </div>
  );
}
function Unavailable({ detail }: { detail?: string }) { return <PageState className="min-h-72" detail={detail ?? "The data governance state could not be loaded."} kind="unavailable" title="Data control plane unavailable" />; }
function scopeLabel(kind: string, ref?: string) {
  return kind === "tenant"
    ? "Workspace"
    : `${kind.replaceAll("_", " ")} · ${ref || "missing reference"}`;
}
function formatDate(value: string) {
  return new Intl.DateTimeFormat("en", {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone: "UTC",
  }).format(new Date(value));
}
