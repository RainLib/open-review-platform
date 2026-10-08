import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import { ExternalLink } from "lucide-react";
import Link from "next/link";

import { AgentWorkManager } from "@/components/console/agent-work-manager";
import {
  DataFreshness,
  PageState,
  RecoveryAction,
} from "@/components/console/page-state";
import { getAgentTaskData, getAgentTaskPolicyData, getPlatformHealthData, getProviderInstallationData, getReviewConfigData } from "@/lib/control-api";
import { agentDraftReviewReadiness, agentInstallationForPolicy, agentInstallationForTask, agentProviderReadiness, agentRepositoryAdmissionReadiness } from "@/lib/agent-task-data";
import { getDeploymentProfile } from "@/lib/deployment-profile";

export default async function AgentWorkPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ task?: string; cursor?: string }>;
}) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const [data, health] = await Promise.all([
    getAgentTaskData(org, query.task, query.cursor),
    getPlatformHealthData(org),
  ]);
  if (data.source !== "live" && data.source !== "partial" && data.source !== "demo")
    return (
      <PageState
        action={
          <RecoveryAction href={`/${encodeURIComponent(org)}/connect`}>
            {t(" Check provider connection ")}</RecoveryAction>
        }
        detail={data.detail ?? t("The control plane is unavailable.")}
        kind="unavailable"
        title={t("Agent work unavailable")}
      />
    );
  const sourceQueue = health.source === "live" ? health.queues.find((queue) => queue.key === "agent-source") : undefined;
  const executionQueue = health.source === "live" ? health.queues.find((queue) => queue.key === "agent-execution") : undefined;
  const executor = health.source === "live" ? health.agent_executor : undefined;
  const decision = health.source === "live" ? health.agent_decision : undefined;
  const credentialBroker = health.source === "live" ? health.agent_credential_broker : undefined;
  const deployment = getDeploymentProfile();
  const selectedTask = data.selected?.task;
  const [exactPolicy, exactInstallation] = selectedTask
    ? await Promise.all([
        getAgentTaskPolicyData(org, selectedTask.provider, selectedTask.api_base_url, selectedTask.repository),
        getProviderInstallationData(org, selectedTask.installation_id),
      ])
    : [undefined, undefined];
  const taskInstallation = selectedTask && exactInstallation?.source === "live" && exactInstallation.installation
    ? agentInstallationForTask(selectedTask, [exactInstallation.installation])
    : undefined;
  const connection = agentProviderReadiness(data, health, selectedTask, exactInstallation);
  const repositoryAdmission = agentRepositoryAdmissionReadiness(data, selectedTask, exactPolicy);
  const reviewPolicy = selectedTask
    ? repositoryAdmission.policy
    : data.policies.find((policy) => policy.mode === "manual" && agentInstallationForPolicy(policy, data.installations));
  const reviewInstallation = selectedTask
    ? taskInstallation
    : reviewPolicy ? agentInstallationForPolicy(reviewPolicy, data.installations) : undefined;
  const [reviewGeneral, reviewFilters] = reviewPolicy && reviewInstallation && data.source !== "demo"
    ? await Promise.all([
        getReviewConfigData(org, "general", "repository", reviewPolicy.repository, reviewPolicy.provider, reviewPolicy.api_base_url),
        getReviewConfigData(org, "filters", "repository", reviewPolicy.repository, reviewPolicy.provider, reviewPolicy.api_base_url),
      ])
    : [undefined, undefined];
  const draftReview = agentDraftReviewReadiness(reviewInstallation, reviewGeneral, reviewFilters, data.selected?.target_branch);
  const reviewConfigQuery = reviewPolicy ? new URLSearchParams({ scope: "repository", repository: reviewPolicy.repository, provider: reviewPolicy.provider, api_base_url: reviewPolicy.api_base_url }) : undefined;
  const draftReviewHref = draftReview.connectionAction && reviewInstallation
    ? `/${encodeURIComponent(org)}/connect/${encodeURIComponent(reviewInstallation.id)}`
    : reviewConfigQuery
      ? `/${encodeURIComponent(org)}/review-config/${draftReview.filtersAction ? "filters" : "general"}?${reviewConfigQuery}`
      : "#agent-repository-admission";
  const decisionStatus = decision?.state === "configured_unverified" ? t("Configured only") : decision?.state === "not_configured" ? t("Not configured") : t("Not observed");
  const executorStatus = executor?.state === "reachable_unverified" ? t("Adapter reachable") : executor?.state === "partially_reachable" ? t("Mixed reachability") : executor?.state === "unreachable" ? t("Adapter unreachable") : executor?.state === "configured_unverified" ? t("Configured only") : executor?.state === "not_configured" ? t("Not configured") : t("Not observed");
  const brokerStatus = credentialBroker?.state === "configured_unverified" ? t("Configured only") : t("Not observed");
  const steps: Array<{ label: string; status: string; detail: string; href: string; action: string; attention?: boolean }> = [
    { label: t("Provider connection"), status: connection.status, detail: connection.detail, href: taskInstallation ? `/${encodeURIComponent(org)}/connect/${encodeURIComponent(taskInstallation.id)}` : `/${encodeURIComponent(org)}/connect`, action: taskInstallation ? t("Connection detail") : t("Connections"), attention: connection.attention },
    { label: t("Repository admission"), status: repositoryAdmission.status, detail: repositoryAdmission.detail, href: "#agent-repository-admission", action: t("Set policy"), attention: repositoryAdmission.attention },
    { label: t("Jev classification"), status: decisionStatus, detail: t("Worker configuration is not proof of a successful model decision."), href: `/${encodeURIComponent(org)}/settings/health?tab=queues`, action: t("Decision health") },
    { label: t("Coding executor"), status: executorStatus, detail: t("A separate sandbox, model broker and scoped write credentials are needed after plan approval."), href: `/${encodeURIComponent(org)}/settings/health?tab=queues`, action: t("Executor health") },
    { label: t("Write-credential broker"), status: brokerStatus, detail: t("A private process heartbeat does not prove scoped token issuance or provider write access."), href: `/${encodeURIComponent(org)}/settings/health?tab=queues`, action: t("Broker health"), attention: health.source === "live" && credentialBroker?.state !== "configured_unverified" },
    { label: t("Draft review handoff"), status: draftReview.status, detail: draftReview.detail, href: draftReviewHref, action: draftReview.connectionAction && reviewInstallation ? t("Connection settings") : reviewPolicy ? draftReview.filtersAction ? t("Review filters") : t("Review settings") : t("Choose repository"), attention: draftReview.attention },
  ];
  return (
    <div className="space-y-5">
      <header className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-[32px] font-semibold leading-[38px] tracking-[-0.045em] text-[var(--ls-text)]">
              {t(" Agent work ")}</h1>
            <DataFreshness language={language} state={data.source} />
          </div>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            {t(" A governed Issue-to-PR control plane. Every request is classified first, then requires a bounded plan and explicit approval before a separately deployed coding executor can receive it. ")}</p>
        </div>
        <Link
          className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-semibold text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]"
          href={`/${encodeURIComponent(org)}/provider-issues`}
        >
          <ExternalLink className="size-4 text-[var(--ls-accent)]" />
          {t(" Provider Issue triage ")}</Link>
      </header>
      <section aria-labelledby="agent-readiness-title" className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
        <div className="border-b border-[var(--ls-line)] px-5 py-4">
          <h2 className="text-base font-semibold text-[var(--ls-text)]" id="agent-readiness-title">{t("Before the first Agent task")}</h2>
          <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">{t("Six independent gates. Creating a Draft does not guarantee review admission or a completed feedback cycle.")}</p>
        </div>
        <ol className="grid gap-px bg-[var(--ls-line)] sm:grid-cols-2 xl:grid-cols-3">
          {steps.map((step, index) => (
            <li className="min-w-0 bg-[var(--ls-surface)] px-5 py-4" key={step.label}>
              <div className="flex flex-col items-start gap-2">
                <span className="text-xs font-semibold tracking-wide text-[var(--ls-text-tertiary)]">0{index + 1} · {t(step.label)}</span>
                <span className={`rounded-full px-2.5 py-1 text-[11px] font-semibold ${step.attention ? "bg-amber-500/[0.09] text-[var(--ls-warning-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]"}`}>{t(step.status)}</span>
              </div>
              <p className="mt-3 min-h-12 text-xs leading-5 text-[var(--ls-text-secondary)]">{t(step.detail)}</p>
              <Link className="luminous-focus mt-2 inline-flex min-h-8 items-center text-xs font-semibold text-[var(--ls-accent)] hover:underline" href={step.href}>{t(step.action)} <span aria-hidden="true" className="ml-1">→</span></Link>
            </li>
          ))}
        </ol>
        <details className="border-t border-[var(--ls-line)] px-5 py-3 text-xs text-[var(--ls-text-secondary)]">
          <summary className="luminous-focus w-fit cursor-pointer font-semibold text-[var(--ls-text)]">{t("Queue and worker evidence")}</summary>
          <div className="mt-3 space-y-1 leading-5">
            <p>{t("Jev: ")}{decision?.detail ?? t("Fresh source-worker configuration evidence is unavailable.")}</p>
            <p>{t("Executor: ")}{executor?.detail ?? t("Fresh runner configuration evidence is unavailable.")}</p>
            <p>{t("Write credentials: ")}{credentialBroker?.detail ?? t("Fresh private broker heartbeat evidence is unavailable.")}</p>
            {sourceQueue && executionQueue ? (
              <p>{t("Source: ")}{sourceQueue.ready} {t(" pending · ")}{sourceQueue.failed} {t(" failed. Execution: ")}{executionQueue.ready} {t(" queued · ")}{executionQueue.running} {t(" running · ")}{executionQueue.failed} {t(" needing attention.")}</p>
            ) : <p>{t("Tenant queue health is unavailable; zero visible tasks does not prove a healthy adapter.")}</p>}
            <p>{t("A signed adapter probe proves endpoint reachability only. Configuration, queue rows, and probe results do not prove model quality, sandbox isolation, provider write access, or a completed Draft PR.")}</p>
          </div>
        </details>
      </section>
      {health.source === "live" && executor?.state === "not_configured" ? (
        <aside className="rounded-[14px] border border-[color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color-mix(in_srgb,var(--ls-warning)_6%,var(--ls-surface))] px-5 py-4 text-sm leading-6 text-[var(--ls-text-secondary)]" role="status">
          <p className="font-semibold text-[var(--ls-text)]">{t("Coding Agent is not connected")}</p>
          <p className="mt-1">
            {t(" Jev classifies an Issue; it cannot write code. A Manual repository policy may create a candidate, but approving a plan without a coding adapter will not produce a Draft PR/MR. ")}</p>
          {deployment.mode === "self_hosted" ? (
            <details className="mt-2 text-xs leading-5">
              <summary className="luminous-focus w-fit cursor-pointer font-semibold text-[var(--ls-accent)]">{t("Self-hosted operator setup")}</summary>
              <p className="mt-2">{t("Configure the separate runner connection (")}<code>AGENT_TASK_ADAPTER_URL</code> {t(" and ")}<code>AGENT_TASK_ADAPTER_SECRET</code>{t("), a reviewed per-job sandbox image, the Codex Responses model route/key, and an installation-scoped provider write credential. Keep all secrets on their designated server-side workers; do not enter them in the Console.")}</p>
              <p className="mt-1">{t("Then confirm adapter reachability in Platform health and test one explicitly approved Issue before enabling labeled-Issue admission.")}</p>
            </details>
          ) : (
            <p className="mt-2 text-xs">{t("Ask the deployment operator to connect the isolated coding adapter; workspace policy alone cannot enable it.")}</p>
          )}
        </aside>
      ) : null}
      <AgentWorkManager data={data} org={org} />
    </div>
  );
}
