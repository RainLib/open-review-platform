"use client";

import { useUiLanguage, useWorkflowStatus, useWorkflowText } from "@/components/console/ui-language-context";

import Link from "next/link";
import { useCallback, useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  Bot,
  CheckCircle2,
  ChevronRight,
  ClipboardCheck,
  ExternalLink,
  LoaderCircle,
  RefreshCw,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import type {
  AgentTask,
  AgentTaskData,
  AgentTaskDetail,
  AgentTaskPolicy,
  AgentTaskPlanSections,
  ProviderInstallation,
  ProviderRepository,
} from "@/lib/control-api";
import { agentAdmissionEvidence, agentAttemptEvidence, agentInstallationForPolicy, agentLinkedReviewOutcome, agentPlanActions, agentPlanHistoricalNotice, agentPlanSectionsValid, agentTaskEmptyQueueStage, agentTaskNeedsLiveRefresh, agentTaskOriginGuidance } from "@/lib/agent-task-data";
import { ProviderMark } from "@/components/providers/provider-icons";
import { CopyEvidenceButton } from "@/components/console/copy-evidence-button";
import { providerIssueTarget, providerReviewCommentTarget, providerReviewTarget } from "@/lib/provider-review-url";
import { cn } from "@/lib/utils";
import { formatTime } from "@/lib/format";

type Notice = { tone: "error" | "success"; message: string } | undefined;

export function AgentWorkManager({
  data,
  org,
}: {
  data: AgentTaskData;
  org: string;
}) {
  const t = useWorkflowText();
  const router = useRouter();
  const [notice, setNotice] = useState<Notice>();
  const [pending, startTransition] = useTransition();
  const isOperational = data.source === "live" || data.source === "partial";
  const watching = isOperational && agentTaskNeedsLiveRefresh(data.tasks, data.selected?.task);
  const emptyQueueStage = agentTaskEmptyQueueStage(data);

  const refresh = () => startTransition(() => router.refresh());

  useEffect(() => {
    if (!watching || pending) return;
    let lastRefresh = Date.now();
    const refreshIfVisible = () => {
      if (document.visibilityState !== "visible" || Date.now() - lastRefresh < 8_000) return;
      lastRefresh = Date.now();
      startTransition(() => router.refresh());
    };
    const interval = window.setInterval(refreshIfVisible, 8_000);
    document.addEventListener("visibilitychange", refreshIfVisible);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", refreshIfVisible);
    };
  }, [pending, router, startTransition, watching]);

  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-5 xl:grid-cols-[minmax(0,1fr)_430px]">
      <div className="min-w-0 space-y-5">
        {notice ? <NoticeBanner notice={notice} /> : null}
        {data.source === "partial" ? (
          <div className="flex flex-wrap items-center gap-3 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.045] px-4 py-3 text-sm text-[var(--ls-text-secondary)]" role="status">
            <AlertTriangle className="size-4 shrink-0 text-[var(--ls-warning-text)]" />
            <p className="min-w-0 flex-1">{data.detail}</p>
            <button className="luminous-focus rounded-[9px] border border-[var(--ls-line-strong)] px-3 py-1.5 text-xs font-semibold text-[var(--ls-text)] disabled:opacity-50" disabled={pending} onClick={refresh} type="button">
              {pending ? t("Refreshing…") : t("Retry unavailable sections")}
            </button>
          </div>
        ) : null}
        <section className="scroll-mt-24 overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]" id="agent-repository-admission">
          <div className="flex flex-col gap-3 border-b border-[var(--ls-line)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 className="text-base font-semibold text-[var(--ls-text)]">
                {t(" Repository admission ")}</h2>
              <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">
                {t(" No policy or Reserved mode admits tasks. Manual mode still requires an approved plan before a coding Agent can start. ")}</p>
            </div>
            <span className="inline-flex w-fit items-center gap-1.5 rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs font-medium text-[var(--ls-text-secondary)]">
              <ShieldCheck className="size-3.5 text-[var(--ls-accent)]" />{" "}
              {t(" policy first ")}</span>
          </div>
          <PolicyForm
            disabled={!isOperational || pending || !data.availability.installations || !data.availability.policies}
            onSaved={() => {
              setNotice({
                tone: "success",
                message:
                  t("Repository policy saved. Existing tasks remain subject to their frozen classification and plan approval."),
              });
              refresh();
            }}
            org={org}
            installations={data.installations}
            policies={data.policies}
          />
          {data.policies.length ? (
            <div className="divide-y divide-[var(--ls-line)] border-t border-[var(--ls-line)]">
              {data.policies.length >= 100 ? (
                <p className="px-5 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">{t("Showing the first 100 policies. Search and select any authorized repository above to load its exact policy and revision before editing.")}</p>
              ) : null}
              {data.policies.map((policy) => (
                <PolicyRow key={policy.id} policy={policy} />
              ))}
            </div>
          ) : data.availability.policies ? (
            <div className="space-y-2 px-5 py-5 text-sm text-[var(--ls-text-secondary)]">
              <p>{t("No repositories are enabled for Agent tasks.")}</p>
              {data.availability.installations && data.installations.length === 0 ? (
                <Link className="luminous-focus inline-flex min-h-10 items-center rounded-[9px] font-semibold text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/connect`}>
                  {t(" Connect and verify a Git provider ")}<ChevronRight className="ml-1 size-4" />
                </Link>
              ) : null}
            </div>
          ) : (
            <p className="px-5 py-5 text-sm text-[var(--ls-warning-text)]">
              {t(" Repository policies could not be loaded. Editing is paused until their revisions are available. ")}</p>
          )}
        </section>

        <section className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
          <div className="flex items-start justify-between gap-4 border-b border-[var(--ls-line)] px-5 py-4">
            <div>
              <h2 className="text-base font-semibold text-[var(--ls-text)]">
                {t(" Agent task queue ")}</h2>
              <p className="mt-1 text-sm text-[var(--ls-text-secondary)]">
                {t(" Created by a verified Issue command, an explicitly enabled Issue label, a bounded revise command on an Agent Draft, or a governed API request. No CLI execution occurs here. ")}</p>
              {watching ? <p className="mt-1 text-xs text-[var(--ls-accent)]">{t("Live progress updates every 8 seconds while this tab is visible.")}</p> : null}
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs font-semibold tabular-nums text-[var(--ls-text-secondary)]">
                {data.tasks.length} {t(" on page ")}</span>
              <button
                aria-label={t("Refresh Agent tasks")}
                className="luminous-focus inline-flex size-8 items-center justify-center rounded-[9px] border border-[var(--ls-line-strong)] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] disabled:opacity-50"
                disabled={pending}
                onClick={refresh}
                title={t("Refresh task status now")}
                type="button"
              >
                <RefreshCw className={cn("size-3.5", pending && "animate-spin")} />
              </button>
            </div>
          </div>
          {data.tasks.length ? (
            <div className="divide-y divide-[var(--ls-line)]">
              {data.tasks.map((task) => (
                <TaskRow
                  href={`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(task.id)}${data.taskCursor ? `&cursor=${encodeURIComponent(data.taskCursor)}` : ""}`}
                  key={task.id}
                  selected={data.selected?.task.id === task.id}
                  task={task}
                />
              ))}
            </div>
          ) : data.availability.tasks && data.taskCursor ? (
            <p className="px-5 py-8 text-sm text-[var(--ls-text-secondary)]">{t("No older Agent tasks on this page. Return to the latest tasks below.")}</p>
          ) : data.availability.tasks ? (
            <div className="px-5 py-8 text-sm text-[var(--ls-text-secondary)]">
              <p className="font-medium text-[var(--ls-text)]">{t("No Agent tasks yet")}</p>
              {emptyQueueStage === "connect" ? (
                <p className="mt-1">{t("Connect and verify GitHub or GitLab before admitting Issue work. ")}<Link className="luminous-focus font-semibold text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/connect`}>{t("Open Connections")}</Link></p>
              ) : emptyQueueStage === "enable" ? (
                <p className="mt-1">{t("No repository currently accepts Agent task requests. ")}<a className="luminous-focus font-semibold text-[var(--ls-accent)] hover:underline" href="#agent-repository-admission">{t("Select a verified repository and save Manual mode")}</a> {t(" first.")}</p>
              ) : emptyQueueStage === "check_repository" ? (
                <p className="mt-1">{t("The policy overview reached its 100-item limit, so it cannot confirm whether another repository is enabled. ")}<a className="luminous-focus font-semibold text-[var(--ls-accent)] hover:underline" href="#agent-repository-admission">{t("Search and select the repository")}</a> {t(" to load its exact policy.")}</p>
              ) : emptyQueueStage === "request" ? (
                <p className="mt-1">{t("In a repository with Manual mode enabled, comment ")}<code className="rounded bg-[var(--ls-surface-muted)] px-1.5 py-0.5">@openreview implement</code> {t(" on a normal Issue. The bot records the request, classifies the frozen Issue snapshot, and waits for a bounded plan and approval.")}</p>
              ) : (
                <p className="mt-1 text-[var(--ls-warning-text)]">{t("Agent admission prerequisites could not be checked. Retry the unavailable provider connection or policy read before issuing a command.")}</p>
              )}
            </div>
          ) : (
            <p className="px-5 py-8 text-sm text-[var(--ls-warning-text)]">
              {t(" The task queue could not be loaded. This is not an empty queue; retry without changing repository policy. ")}</p>
          )}
          {data.taskCursor || (data.availability.tasks && data.nextTaskCursor) ? (
            <nav aria-label={t("Agent task history")} className="flex items-center justify-between gap-3 border-t border-[var(--ls-line)] px-5 py-3 text-xs font-semibold">
              {data.taskCursor ? <Link className="luminous-focus text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/agent-work`}>{t("Latest tasks")}</Link> : <span />}
              {data.nextTaskCursor ? <Link className="luminous-focus text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/agent-work?cursor=${encodeURIComponent(data.nextTaskCursor)}`}>{t("Older tasks →")}</Link> : <span className="text-[var(--ls-text-tertiary)]">{t("End of history")}</span>}
            </nav>
          ) : null}
        </section>
      </div>
      <TaskInspector
        detail={data.selected}
        disabled={!isOperational || pending}
        key={data.selected?.task.id ?? "none"}
        onChanged={(message) => {
          setNotice({ tone: "success", message });
          refresh();
        }}
        org={org}
        selectionUnavailable={data.selectedTaskRequested && !data.availability.selected}
      />
    </div>
  );
}

function agentInstallationLabel(installation: ProviderInstallation): string {
  let endpoint = installation.api_base_url;
  try {
    const url = new URL(installation.api_base_url);
    endpoint = `${url.host}${url.pathname.replace(/\/api\/v4\/?$/, "").replace(/\/$/, "")}`;
  } catch {
    // Keep the original control-plane value visible if a legacy URL is malformed.
  }
  const provider = installation.provider === "github" ? "GitHub" : "GitLab";
  return `${provider} · ${installation.repository_scope} · ${endpoint} · ${installation.id.slice(-8)}`;
}

function PolicyForm({
  disabled,
  installations,
  policies,
  onSaved,
  org,
}: {
  disabled: boolean;
  installations: ProviderInstallation[];
  policies: AgentTaskPolicy[];
  onSaved: () => void;
  org: string;
}) {
  const t = useWorkflowText();
  const [installationID, setInstallationID] = useState(
    installations[0]?.id ?? "",
  );
  const [repositorySearch, setRepositorySearch] = useState("");
  const searchTerm = repositorySearch.trim();
  const [inventory, setInventory] = useState<{
    installationID: string;
    query: string;
    repositories: ProviderRepository[];
    state: "ready" | "unavailable";
  }>();
  const [repository, setRepository] = useState("");
  const [selectedPolicyID, setSelectedPolicyID] = useState("");
  const [policyLookup, setPolicyLookup] = useState<{
    identity: string;
    state: "ready" | "unavailable";
    policy?: AgentTaskPolicy;
  }>();
  const [policyLookupAttempt, setPolicyLookupAttempt] = useState(0);
  const [mode, setMode] = useState<AgentTaskPolicy["mode"]>("disabled");
  const [maxAttempts, setMaxAttempts] = useState(1);
  const [workflowEnabled, setWorkflowEnabled] = useState(false);
 const [requireCriterionEvidence,setRequireCriterionEvidence]=useState(true);
  const [repairCycles, setRepairCycles] = useState(2);
  const [taskAttempts, setTaskAttempts] = useState(3);
  const [requiredChecks, setRequiredChecks] = useState("");
  const [maxExecutionSeconds, setMaxExecutionSeconds] = useState(1800);
  const [maxFeedbackCycles, setMaxFeedbackCycles] = useState(0);
  const [executorProfile, setExecutorProfile] = useState<"codex" | "claude">("codex");
  const [decisionBackend, setDecisionBackend] = useState<AgentTaskPolicy["decision_backend"]>("jev");
  const [autoAdmissionEnabled, setAutoAdmissionEnabled] = useState(false);
  const [autoAdmissionLabel, setAutoAdmissionLabel] = useState("openreview:implement");
  const [message, setMessage] = useState<string>();
  const [pending, startTransition] = useTransition();
  const installation =
    installations.find((item) => item.id === installationID) ??
    installations[0];
  const selectedInstallationID = installation?.id;
  const provider = installation?.provider ?? "github";
  const baseURL = installation?.api_base_url ?? "";
  const currentInventory =
    inventory?.installationID === selectedInstallationID && inventory.query === searchTerm ? inventory : undefined;
  const repositories = currentInventory?.repositories ?? [];
  const inventoryState = !selectedInstallationID
    ? "unavailable"
    : currentInventory?.state ?? "loading";

  const policyIdentity = repository ? `${provider}\u0000${baseURL.replace(/\/$/, "")}\u0000${repository}` : "";
  const currentPolicyLookup = policyLookup?.identity === policyIdentity ? policyLookup : undefined;
  const policyReady = currentPolicyLookup?.state === "ready";
  const matchingPolicy = policyReady ? currentPolicyLookup.policy : undefined;

  useEffect(() => {
    if (!selectedInstallationID) return;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => {
      const parameters = new URLSearchParams();
      if (searchTerm) parameters.set("query", searchTerm);
      fetch(
        `/api/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(selectedInstallationID)}/repositories?${parameters.toString()}`,
        { cache: "no-store", signal: controller.signal },
      )
        .then(async (response) => {
          if (!response.ok) throw new Error("Provider inventory unavailable");
          return (await response.json()) as { repositories?: ProviderRepository[] };
        })
        .then((result) => {
          if (controller.signal.aborted) return;
          setInventory({
            installationID: selectedInstallationID,
            query: searchTerm,
            repositories: (result.repositories ?? []).filter((item) => !item.archived),
            state: "ready",
          });
        })
        .catch(() => {
          if (!controller.signal.aborted) {
            setInventory({
              installationID: selectedInstallationID,
              query: searchTerm,
              repositories: [],
              state: "unavailable",
            });
          }
        });
    }, searchTerm ? 250 : 0);
    return () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [selectedInstallationID, org, searchTerm]);
  const hydrateExecutionEnvelope = useCallback((existing?: AgentTaskPolicy) => {
    // Re-opening an existing repository policy must not make a routine mode
    // change silently reset its execution envelope. A repository without a
    // policy starts from the narrowest safe defaults instead.
    setMode(existing?.mode ?? "disabled");
    setMaxAttempts(existing?.max_attempts ?? 1);
    setWorkflowEnabled(existing?.workflow?.enabled ?? false);
 setRequireCriterionEvidence(existing?.workflow ? existing.workflow.require_criterion_evidence===true : true);
    setRepairCycles(existing?.workflow?.max_repair_cycles ?? 2);
    setTaskAttempts(existing?.workflow?.max_task_attempts ?? 3);
    setRequiredChecks(existing?.workflow?.required_checks?.join(", ") ?? "");
    setMaxExecutionSeconds(existing?.max_execution_seconds ?? 1800);
    setMaxFeedbackCycles(existing?.max_feedback_cycles ?? 0);
    setExecutorProfile(existing?.executor_profile ?? "codex");
    setDecisionBackend(existing?.decision_backend ?? "jev");
    setAutoAdmissionEnabled(existing?.auto_admission_enabled ?? false);
    setAutoAdmissionLabel(existing?.auto_admission_label ?? "openreview:implement");
  }, []);

  // Locale changes only affect rendering. Rehydrating here would discard an
  // unsaved execution envelope when the user switches interface language.
  // Internal lookup errors become a state code; their visible copy is localized below.
  useEffect(() => {
    if (!policyIdentity) return;
    const controller = new AbortController();
    const parameters = new URLSearchParams({ provider, api_base_url: baseURL, repository });
    fetch(`/api/tenants/${encodeURIComponent(org)}/agent-task-policies?${parameters.toString()}`, {
      cache: "no-store",
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) throw new Error("Policy revision unavailable");
        const result = (await response.json()) as { agent_task_policies?: AgentTaskPolicy[] };
        if (!Array.isArray(result.agent_task_policies) || result.agent_task_policies.length > 1) {
          throw new Error("Policy lookup returned an invalid result");
        }
        const policy = result.agent_task_policies[0];
        if (policy && `${policy.provider}\u0000${policy.api_base_url}\u0000${policy.repository}` !== policyIdentity) {
          throw new Error("Policy lookup returned a different repository");
        }
        return policy;
      })
      .then((policy) => {
        if (controller.signal.aborted) return;
        setPolicyLookup({ identity: policyIdentity, state: "ready", policy });
        hydrateExecutionEnvelope(policy);
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setPolicyLookup({ identity: policyIdentity, state: "unavailable" });
        }
      });
    return () => controller.abort();
  }, [baseURL, hydrateExecutionEnvelope, org, policyIdentity, policyLookupAttempt, provider, repository]);
  const repositoryVerified =
    inventoryState === "ready" &&
    repositories.some((item) => item.name === repository);

  const submit = () =>
    startTransition(async () => {
      setMessage(undefined);
      if (!installation || !repositoryVerified) {
        setMessage(t("Select a repository from a verified provider inventory first."));
        return;
      }
      if (!policyReady) {
        setMessage(t("Wait for the exact repository policy revision before saving."));
        return;
      }
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/agent-task-policies`,
          {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              provider,
              api_base_url: baseURL,
              repository,
              mode,
              workflow: workflowEnabled && mode === "manual" ? {enabled:true,require_criterion_evidence:requireCriterionEvidence,max_repair_cycles:repairCycles,max_task_attempts:taskAttempts,required_checks:requiredChecks.split(",").map(value=>value.trim()).filter(Boolean)} : {enabled:false,max_repair_cycles:0,max_task_attempts:0},
max_attempts: maxAttempts,
              max_execution_seconds: maxExecutionSeconds,
              max_feedback_cycles: mode === "manual" ? maxFeedbackCycles : 0,
              executor_profile: executorProfile,
              decision_backend: decisionBackend,
              auto_admission_enabled: autoAdmissionEnabled,
              auto_admission_label: autoAdmissionLabel,
              revision: matchingPolicy?.revision ?? 0,
            }),
          },
        );
        if (!response.ok) {
          if (response.status === 409) {
            setPolicyLookup(undefined);
            setPolicyLookupAttempt((attempt) => attempt + 1);
          }
          setMessage(await mutationError(response, t("The repository policy was not saved.")));
          return;
        }
        const saved = (await response.json()) as AgentTaskPolicy;
        if (`${saved.provider}\u0000${saved.api_base_url}\u0000${saved.repository}` === policyIdentity) {
          setPolicyLookup({ identity: policyIdentity, state: "ready", policy: saved });
        } else {
          setPolicyLookup({ identity: policyIdentity, state: "unavailable" });
        }
        onSaved();
      } catch {
        setMessage(t("The repository policy was not saved. Check the connection and try again."));
      }
    });
  return (
    <form
      action={submit}
      className="grid min-w-0 gap-3 p-4 sm:grid-cols-2"
    >
      {policies.length ? (
        <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)] sm:col-span-2">
          {t(" Edit an existing repository policy ")}<select
            className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]"
            disabled={disabled || pending}
            onChange={(event) => {
              const selected = policies.find((item) => item.id === event.target.value);
              if (!selected) {
                setSelectedPolicyID("");
                return;
              }
              const connected = agentInstallationForPolicy(selected, installations);
              if (!connected) {
                setMessage(t("This policy has no matching verified installation. Reconnect the provider before editing it."));
                setSelectedPolicyID("");
                return;
              }
              setMessage(undefined);
              setSelectedPolicyID(selected.id);
              setInstallationID(connected.id);
              setRepositorySearch(selected.repository);
              setRepository(selected.repository);
              setPolicyLookup(undefined);
              hydrateExecutionEnvelope();
            }}
            value={selectedPolicyID}
          >
            <option value="">{t("Choose an existing policy or search below")}</option>
            {policies.map((policy) => (
              <option key={policy.id} value={policy.id}>
                {policy.provider === "github" ? "GitHub" : "GitLab"} · {policy.repository} · {policy.mode}
              </option>
            ))}
          </select>
          <span className="text-[11px] font-normal text-[var(--ls-text-tertiary)]">{t("Selection verifies the repository inventory and reloads the exact policy revision before saving.")}</span>
        </label>
      ) : null}
      <div className="min-w-0">
        <select
          aria-label={t("Verified provider installation")}
          className="luminous-focus h-10 w-full min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm"
          disabled={disabled || pending || installations.length === 0}
          onChange={(event) => {
            const next = installations.find(
              (item) => item.id === event.target.value,
            );
            setInstallationID(event.target.value);
            setRepository("");
            setRepositorySearch("");
            setSelectedPolicyID("");
            setPolicyLookup(undefined);
            if (next) hydrateExecutionEnvelope();
          }}
          value={installation?.id ?? ""}
        >
          {installations.length === 0 ? (
            <option value="">{t("No verified connections")}</option>
          ) : null}
          {installations.map((item) => (
            <option key={item.id} value={item.id}>
              {agentInstallationLabel(item)}
            </option>
          ))}
        </select>
        {installation ? (
          <p className="mt-1 truncate px-1 text-[11px] text-[var(--ls-text-tertiary)]" title={`${installation.api_base_url} · installation ${installation.id}`}>
            {t(" Authorized scope: ")}{installation.repository_scope} {t(" · installation ")}{installation.id.slice(-8)}
          </p>
        ) : null}
      </div>
      <fieldset className="grid gap-3 rounded-xl border border-[var(--ls-line)] p-4 sm:col-span-2">
        <legend className="px-1 text-sm font-semibold">{t("Task delivery workflow")}</legend>
        <label className="flex items-center gap-3 text-sm"><input type="checkbox" checked={workflowEnabled} disabled={disabled || pending || mode !== "manual"} onChange={event=>setWorkflowEnabled(event.target.checked)} />{t("Auto-plan, verify and repair, re-review, then human acceptance")}</label>
        {workflowEnabled ? <>
          <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={requireCriterionEvidence} disabled={disabled || pending} onChange={event=>setRequireCriterionEvidence(event.target.checked)} />{t("Require independent evidence for every acceptance criterion")}</label>
          <p className="text-xs leading-5">{t("The fixed verifier must report a passing result for each criterion. A general test-suite pass alone cannot satisfy this option.")}</p>
          <label className="grid gap-1 text-xs">{t("Verification repair cycles")}<input className="luminous-focus h-10 rounded-lg border border-[var(--ls-line)] px-3" type="number" min={0} max={3} value={repairCycles} disabled={disabled || pending} onChange={event=>setRepairCycles(Number(event.target.value))} /></label>
          <label className="grid gap-1 text-xs">{t("Total attempts across plans and feedback")}<input className="luminous-focus h-10 rounded-lg border border-[var(--ls-line)] px-3" type="number" min={1} max={5} value={taskAttempts} disabled={disabled || pending} onChange={event=>setTaskAttempts(Number(event.target.value))} /></label>
          <label className="grid gap-1 text-xs">{t("Required independent CI checks, separated by commas")}<input className="luminous-focus h-10 rounded-lg border border-[var(--ls-line)] px-3" value={requiredChecks} maxLength={1800} disabled={disabled || pending} onChange={event=>setRequiredChecks(event.target.value)} placeholder="CI / tests, CI / build" /></label>
          <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">{t("New tasks require a deployment-approved verification profile. Initial and repair plans still need owner/admin approval. Blocking findings, diagnosed code CI failures and owner acceptance feedback prepare repair tasks automatically. A blank CI list requires at least one independent check and all observed independent checks to pass. Existing task budgets stay frozen.")}</p>
        </> : null}
      </fieldset>
      <select
        aria-label={t("Agent executor profile")}
        className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm"
        disabled={disabled || pending}
        onChange={(event) =>
          setExecutorProfile(event.target.value as "codex" | "claude")
        }
        title={t("Frozen into each admitted Agent task")}
        value={executorProfile}
      >
        <option value="codex">Codex</option>
        <option value="claude">Claude CLI</option>
      </select>
      <label className="grid gap-1.5 text-xs font-medium text-[var(--ls-text-secondary)] sm:col-span-2">
        {t(" Search authorized repositories ")}<input
          className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)]"
          disabled={disabled || pending || !selectedInstallationID}
          maxLength={120}
          onChange={(event) => {
            setRepositorySearch(event.target.value);
            setRepository("");
            setSelectedPolicyID("");
            setPolicyLookup(undefined);
            hydrateExecutionEnvelope();
          }}
          placeholder={t("Search by repository name, including beyond the first 500")}
          type="search"
          value={repositorySearch}
        />
        <span className="text-[11px] font-normal text-[var(--ls-text-tertiary)]">{t("Showing up to 500 matches from this installation. Narrow the search to find larger inventories.")}</span>
      </label>
      <select
        aria-label={t("Verified repository")}
        className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm"
        disabled={disabled || pending || inventoryState !== "ready" || repositories.length === 0}
        onChange={(event) => {
          setRepository(event.target.value);
          setSelectedPolicyID("");
          setPolicyLookup(undefined);
          hydrateExecutionEnvelope();
        }}
        required
        value={repository}
      >
        <option value="">
          {inventoryState === "loading"
            ? t("Loading repositories…")
            : inventoryState === "unavailable"
              ? t("Repository inventory unavailable")
              : repositories.length === 0
                ? searchTerm ? t("No matching repositories") : t("No active repositories")
                : t("Select a repository")}
        </option>
        {repositories.map((item) => (
          <option key={item.external_id} value={item.name}>
            {item.name}
          </option>
        ))}
      </select>
      <select
        aria-label={t("Agent task admission mode")}
        className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm"
        disabled={disabled || pending}
        onChange={(event) =>
          {
            const nextMode = event.target.value as AgentTaskPolicy["mode"];
            setMode(nextMode);
            if (nextMode !== "manual") setAutoAdmissionEnabled(false);
          }
        }
        value={mode}
      >
        <option value="disabled">{t("Disabled")}</option>
        <option value="suggest">{t("Reserved — no tasks")}</option>
        <option value="manual">{t("Manual")}</option>
      </select>
      <div className="relative">
        <label
          className="pointer-events-none absolute left-3 top-1/2 z-10 -translate-y-1/2 text-xs font-medium text-[var(--ls-text-tertiary)]"
          htmlFor="agent-max-attempts"
        >
          {t(" Attempts ")}</label>
        <input
          aria-label={t("Maximum attempts")}
          className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] py-0 pl-[74px] pr-3 text-right text-sm"
          disabled={disabled || pending}
          id="agent-max-attempts"
          max={3}
          min={1}
          onChange={(event) => setMaxAttempts(Number(event.target.value))}
          title={t("Maximum isolated executions for one approved plan")}
          type="number"
          value={maxAttempts}
        />
      </div>
      <div className="relative">
        <label
          className="pointer-events-none absolute left-3 top-1/2 z-10 -translate-y-1/2 text-xs font-medium text-[var(--ls-text-tertiary)]"
          htmlFor="agent-max-execution-minutes"
        >
          {t(" Minutes ")}</label>
        <input
          aria-label={t("Maximum execution minutes")}
          className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] py-0 pl-[68px] pr-3 text-right text-sm"
          disabled={disabled || pending}
          id="agent-max-execution-minutes"
          max={120}
          min={1}
          onChange={(event) =>
            setMaxExecutionSeconds(Number(event.target.value) * 60)
          }
          step={1}
          title={t("Maximum wall-clock time for one isolated execution")}
          type="number"
          value={Math.floor(maxExecutionSeconds / 60)}
        />
      </div>
      {message ? (
        <p className="sm:col-span-2 text-xs text-[var(--ls-danger-text)]" role="alert">
          {message}
        </p>
      ) : null}
      {repositoryVerified && !policyReady ? (
        <div className="flex items-center gap-2 text-xs text-[var(--ls-text-secondary)] sm:col-span-2" role="status">
          <span>{currentPolicyLookup?.state === "unavailable" ? t("Policy revision unavailable; saving is paused.") : t("Loading the exact repository policy revision…")}</span>
          {currentPolicyLookup?.state === "unavailable" ? (
            <button className="luminous-focus font-semibold text-[var(--ls-accent)] hover:underline" onClick={() => { setPolicyLookup(undefined); setPolicyLookupAttempt((attempt) => attempt + 1); }} type="button">{t("Retry")}</button>
          ) : null}
        </div>
      ) : null}
      <div className="flex flex-wrap items-center gap-3 border-t border-[var(--ls-line)] pt-3 text-xs text-[var(--ls-text-secondary)] sm:col-span-2">
        <label className="flex items-center gap-2 font-medium text-[var(--ls-text)]">
          {t(" Decision backend ")}<select
            aria-label={t("Agent task decision backend")}
            className="luminous-focus h-9 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs"
            disabled={disabled || pending}
            onChange={(event) => setDecisionBackend(event.target.value as AgentTaskPolicy["decision_backend"])}
            value={decisionBackend}
          >
          <option value="jev">{t("TypeSafe Jev (default)")}</option>
            <option value="deterministic">{t("Rules only (legacy/offline)")}</option>
          </select>
        </label>
        <span className="leading-5 text-[var(--ls-text-tertiary)]">
          {t(" Hosted Jev sends verified Issue evidence to TypeSafe. Configure the selected endpoint on the source worker; a missing or invalid response stops admission. No automatic execution is enabled. ")}</span>
      </div>
      <div className="flex flex-wrap items-center gap-3 border-t border-[var(--ls-line)] pt-3 text-xs text-[var(--ls-text-secondary)] sm:col-span-2">
        <label className="flex items-center gap-2 font-medium text-[var(--ls-text)]">
          <input
            checked={autoAdmissionEnabled}
            className="size-4 accent-[var(--ls-accent)]"
            disabled={disabled || pending || mode !== "manual"}
            onChange={(event) => setAutoAdmissionEnabled(event.target.checked)}
            type="checkbox"
          />
          {t(" Create candidate from labeled Issues ")}</label>
        <input
          aria-label={t("Automatic Agent admission label")}
          className="luminous-focus h-9 min-w-[190px] rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs"
          disabled={disabled || pending || !autoAdmissionEnabled || mode !== "manual"}
          maxLength={128}
          onChange={(event) => setAutoAdmissionLabel(event.target.value)}
          placeholder="openreview:implement"
          value={autoAdmissionLabel}
        />
        <span className="max-w-2xl leading-5 text-[var(--ls-text-tertiary)]">
          {t(" Creates only a governed candidate. Source capture, decision evaluation, and owner/admin plan approval still block every executor. ")}</span>
      </div>
      <p className="sm:col-span-2 text-xs leading-5 text-[var(--ls-text-tertiary)]">
        {t("Frozen limits: {attempts} isolated execution(s), up to {minutes} minutes each, using {executor}. Manual mode requires classification, a bounded plan and owner/admin approval.", { attempts: maxAttempts, minutes: Math.floor(maxExecutionSeconds / 60), executor: executorProfile })}</p>
	  <label className="grid gap-1.5 text-sm font-medium text-[var(--ls-text)] sm:col-span-2">
		{t(" Bounded manual Draft PR feedback cycles ")}<select
		  className="h-10 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-55"
		  disabled={disabled || pending || mode !== "manual"}
		  onChange={(event) => setMaxFeedbackCycles(Number(event.target.value))}
		  value={mode === "manual" ? maxFeedbackCycles : 0}
		>
		  <option value={0}>{t("Disabled")}</option>
		  <option value={1}>{t("One revision")}</option>
		  <option value={2}>{t("Two revisions")}</option>
		  <option value={3}>{t("Three revisions")}</option>
		</select>
		<span className="text-xs font-normal leading-5 text-[var(--ls-text-tertiary)]">
		  {t(" A trusted ")}<code>@openreview revise …</code> {t(" on the Draft PR starts a new plan-and-approval cycle; it never auto-runs or merges. ")}</span>
	  </label>
      <button
        className="luminous-focus h-10 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-50 sm:col-span-2"
        disabled={disabled || pending || !repositoryVerified || !policyReady}
        type="submit"
      >
        {pending ? t("Saving…") : t("Save repository policy")}
      </button>
    </form>
  );
}

function PolicyRow({ policy }: { policy: AgentTaskPolicy }) {
  const t = useWorkflowText();
  return (
    <div className="flex items-center gap-3 px-5 py-3">
      <ProviderMark className="size-4" provider={policy.provider} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-[var(--ls-text)]">
          {policy.repository}
        </p>
        <p className="truncate text-xs text-[var(--ls-text-tertiary)]">
          {policy.api_base_url}
        </p>
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
          {policy.executor_profile} · {policy.decision_backend} {t(" decision · ")}{policy.max_attempts} {policy.max_attempts === 1 ? t("attempt") : t("attempts")} ·{" "}
          {Math.floor(policy.max_execution_seconds / 60)} {t(" min maximum ")}{policy.max_feedback_cycles ? t(" · {count} feedback cycle(s)", { count: policy.max_feedback_cycles }) : t(" · manual feedback disabled")}
        </p>
      </div>
      <ModePill mode={policy.mode} />
    </div>
  );
}

function TaskRow({
  href,
  selected,
  task,
}: {
  href: string;
  selected: boolean;
  task: AgentTask;
}) {
  const t = useWorkflowText();
  return (
    <Link
      aria-current={selected ? "page" : undefined}
      className={cn(
        "flex w-full items-center gap-3 px-5 py-4 text-left transition hover:bg-[var(--ls-surface-muted)]",
        selected && "bg-[color-mix(in_srgb,var(--ls-accent)_8%,transparent)]",
      )}
      href={href}
      scroll={false}
    >
      <ProviderMark className="size-4 shrink-0" provider={task.provider} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-semibold text-[var(--ls-text)]">
          {task.repository}
          <span className="ml-1.5 font-normal text-[var(--ls-text-secondary)]">
            #{task.origin_number}
          </span>
        </p>
        <p className="mt-1 truncate text-xs text-[var(--ls-text-tertiary)]">
          {task.origin_kind === "pull_request" ? t("pull request") : "Issue"} {t(" · revision ")}{task.revision} {t(" · requested by ")}{task.requested_by}
        </p>
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
          {t(" frozen envelope · ")}{task.executor_profile} · {task.max_attempts} {task.max_attempts === 1 ? t("attempt") : t("attempts")} ·{" "}
          {Math.floor(task.max_execution_seconds / 60)} {t(" min ")}{task.feedback_cycle ? t(" · feedback depth {depth}", { depth: task.feedback_cycle }) : ""}
        </p>
      </div>
      <StatePill state={task.state} />
      <ChevronRight className="size-4 text-[var(--ls-text-tertiary)]" />
    </Link>
  );
}

function TaskInspector({
  detail,
  disabled,
  onChanged,
  org,
  selectionUnavailable,
}: {
  detail?: AgentTaskDetail;
  disabled: boolean;
  onChanged: (message: string) => void;
  org: string;
  selectionUnavailable: boolean;
}) {
  const t = useWorkflowText();
  const language = useUiLanguage();
  const status = useWorkflowStatus();
  if (selectionUnavailable)
    return (
      <aside className="h-fit rounded-[18px] border border-amber-500/25 bg-amber-500/[0.045] p-6 text-sm text-[var(--ls-text-secondary)]">
        <AlertTriangle className="size-5 text-[var(--ls-warning-text)]" />
        <h2 className="mt-4 font-semibold text-[var(--ls-text)]">{t("Task detail unavailable")}</h2>
        <p className="mt-1 leading-6">{t("The selected task could not be loaded. The task queue and repository policies remain independent.")}</p>
        <Link className="luminous-focus mt-4 inline-flex rounded-[9px] border border-[var(--ls-line-strong)] px-3 py-2 text-xs font-semibold text-[var(--ls-text)]" href={`/${encodeURIComponent(org)}/agent-work`}>{t("Back to task queue")}</Link>
      </aside>
    );
  if (!detail)
    return (
      <aside className="rounded-[18px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] p-6 text-sm text-[var(--ls-text-secondary)]">
        <Bot className="size-5 text-[var(--ls-accent)]" />
        <h2 className="mt-4 font-semibold text-[var(--ls-text)]">
          {t(" Select an Agent task ")}</h2>
        <p className="mt-1 leading-6">
          {t(" Classification evidence, plans and owner approval stay together with the exact provider source revision. ")}</p>
      </aside>
    );
  const classification = detail.classifications[0];
  const originGuidance = agentTaskOriginGuidance(detail.task.origin_kind);
  const admissionEvidence = classification ? agentAdmissionEvidence(classification.evaluation) : undefined;
  const newestPlan = detail.plans[0];
  const admissionBlocked =
    classification !== undefined && classification.decision !== "requires_human";
  const sourceReady =
    detail.task.source_state === "ready" &&
    Boolean(detail.task.source_base_ref) &&
    Boolean(detail.task.source_base_sha);
  const providerTarget = detail.task.origin_kind === "pull_request"
    ? providerReviewTarget({
        provider: detail.task.provider,
        api_base_url: detail.task.api_base_url,
        repository: detail.task.repository,
        review_number: detail.task.origin_number,
      })
    : providerIssueTarget({
        provider: detail.task.provider,
        api_base_url: detail.task.api_base_url,
        repository: detail.task.repository,
        issue_number: detail.task.origin_number,
      });
  const feedbackTarget = detail.feedback && !detail.feedback.source_review_run_id && !detail.feedback.internal_repair_kind && detail.task.origin_kind === "pull_request"
    ? providerReviewCommentTarget({
        provider: detail.task.provider,
        api_base_url: detail.task.api_base_url,
        repository: detail.task.repository,
        review_number: detail.task.origin_number,
        comment_external_id: detail.feedback.comment_external_id,
      })
    : undefined;
  const reviewSettingsQuery = new URLSearchParams({
    scope: "repository",
    repository: detail.task.repository,
    provider: detail.task.provider,
    api_base_url: detail.task.api_base_url,
  });
  return (
    <aside className="h-fit overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
      <div className="border-b border-[var(--ls-line)] p-5">
        <div className="flex items-start justify-between gap-3">
          <div>
            <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-text-tertiary)]">
              {t(" Agent task ")}</p>
            <h2 className="mt-1 text-base font-semibold text-[var(--ls-text)]">
              {detail.task.repository} #{detail.task.origin_number}
            </h2>
          </div>
          {providerTarget ? (
            <a
              aria-label={t("Open {resource} in {provider}", { resource: detail.task.origin_kind === "pull_request" ? t("pull request") : "Issue", provider: providerTarget.label })}
              className="luminous-focus rounded-[9px] border border-[var(--ls-line-strong)] p-2 text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"
              href={providerTarget.url}
              rel="noreferrer"
              target="_blank"
              title={providerTarget.url}
            >
              <ExternalLink className="size-4" />
            </a>
          ) : null}
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          <StatePill state={detail.task.state} />
          {classification ? (
            <RiskPill risk={classification.risk_level} />
          ) : null}
        </div>
        <div className="mt-3 flex min-w-0 flex-wrap items-center gap-2 text-xs text-[var(--ls-text-tertiary)]">
          <span>{t("Task ID")}</span>
          <code className="min-w-0 break-all font-mono">{detail.task.id}</code>
          <CopyEvidenceButton label={t("Copy task ID")} value={detail.task.id} />
        </div>
        <p className="mt-3 text-xs text-[var(--ls-text-tertiary)]">
          {t(" Frozen policy v")}{detail.task.policy_revision} · {detail.task.decision_backend} {t(" decision · ")}{detail.task.executor_profile} · {detail.task.max_attempts}{" "}
          {detail.task.max_attempts === 1 ? t("attempt") : t("attempts")} {t(" · up to")}{" "}
          {Math.floor(detail.task.max_execution_seconds / 60)} {t(" min per attempt ")}{detail.task.feedback_cycle ? t(" · feedback depth {depth}", { depth: detail.task.feedback_cycle }) : detail.task.max_feedback_cycles ? t(" · up to {count} Draft PR feedback cycles", { count: detail.task.max_feedback_cycles }) : t(" · manual Draft PR feedback disabled")}
        </p>
      </div>
      <div className="space-y-5 p-5">
		{detail.execution_budget ? (
		  <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
			<h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Execution budget · ")}{detail.execution_budget.used}/{detail.execution_budget.limit}</h3>
			<p className="mt-2 text-sm text-[var(--ls-text-secondary)]">
			  {t(" Shared by the original task and all feedback cycles: ")}{detail.execution_budget.successful_deliveries} {detail.execution_budget.successful_deliveries === 1 ? t("successful delivery") : t("successful deliveries")} · {detail.execution_budget.attention_attempts} {detail.execution_budget.attention_attempts === 1 ? t("attempt needs attention") : t("attempts need attention")}.
			</p>
		  </section>
		) : null}
		{detail.execution_block ? (
		  <section role="status" className="rounded-[12px] border border-amber-500/30 bg-amber-500/5 p-4">
			<h3 className="text-sm font-semibold text-[var(--ls-text)]">{t("Execution held before starting")}</h3>
			<p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">{t(detail.execution_block.message)}</p>
			<code className="mt-2 block text-xs text-[var(--ls-text-tertiary)]">{detail.execution_block.code}</code>
		  </section>
		) : null}
        {detail.feedback ? (
          <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
            <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
              <ExternalLink className="size-4 text-[var(--ls-accent)]" />
              {detail.feedback.internal_repair_kind ? t("{kind} repair evidence", { kind: t(detail.feedback.internal_repair_kind) }) : detail.feedback.source_review_run_id ? t("Blocking review result") : t("Original review feedback")}
            </h3>
            <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
              {(detail.feedback.source_review_run_id || detail.feedback.internal_repair_kind)
                ? t("This repair is bound to the retained repair evidence and exact Draft commit. The current Draft is checked again before execution and publication; this plan requires its own approval.")
                : t("This cycle is bound to comment #{comment} by provider user #{actor}. The instruction is checked again before execution and publication.", { comment: detail.feedback.comment_external_id, actor: detail.feedback.actor_external_id })}
            </p>
            {detail.feedback.source_review_run_id ? (
              <Link className="luminous-focus mt-2 inline-flex min-h-8 items-center gap-1.5 text-sm font-semibold text-[var(--ls-accent)]"
                href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(detail.feedback.source_review_run_id)}`}>
                {t(" Inspect the exact review findings ")}</Link>
            ) : feedbackTarget ? (
              <a
                className="luminous-focus mt-2 inline-flex min-h-8 items-center gap-1.5 rounded-[8px] text-sm font-semibold text-[var(--ls-accent)] underline-offset-4 hover:underline"
                href={feedbackTarget.url}
                rel="noreferrer"
                target="_blank"
              >
                {t(" View exact comment in ")}{feedbackTarget.label}
                <ExternalLink className="size-3.5" />
              </a>
            ) : (
              <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">{t("A safe provider comment link is unavailable for this installation.")}</p>
            )}
          </section>
        ) : null}
        {classification ? (
          <section>
            <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
              <ClipboardCheck className="size-4 text-[var(--ls-accent)]" />{" "}
              {t(" Admission decision ")}</h3>
            <p className="mt-2 text-sm font-medium text-[var(--ls-text)]">
              {status(classification.decision)} ·{" "}
              {t(" rule score ")}{classification.confidence}/100
            </p>
            <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
              {t(" Admission action recorded: ")}{status(classification.next_action)} · {classification.classifier_version}
            </p>
            <ul className="mt-2 space-y-1.5 text-sm leading-5 text-[var(--ls-text-secondary)]">
              {classification.reasons.map((reason) => (
                <li key={reason}>• {reason}</li>
              ))}
            </ul>
            {admissionEvidence?.modelAdvisory ? (
              <div className="mt-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2.5 text-xs leading-5 text-[var(--ls-text-secondary)]">
                <p className="font-semibold text-[var(--ls-text)]">{t("Model advisory — not execution approval")}</p>
                <p className="mt-1">{admissionEvidence.modelAdvisory.summary}</p>
                <p className="mt-1 font-mono text-[10px] text-[var(--ls-text-tertiary)]">{admissionEvidence.modelAdvisory.signals.join(" · ")}</p>
              </div>
            ) : null}
            {admissionEvidence?.hardChecks.length ? (
              <details className="mt-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2.5">
                <summary className="cursor-pointer text-xs font-semibold text-[var(--ls-text-secondary)]">
                  {t(" Deterministic admission checks · ")}{admissionEvidence.hardChecks.length}
                </summary>
                <div className="mt-3 space-y-2.5">
                  {admissionEvidence.hardChecks.map((stage) => (
                    <div className="text-xs leading-5 text-[var(--ls-text-secondary)]" key={stage.stage}>
                      <p className="font-medium capitalize text-[var(--ls-text)]">
                        {stage.stage} · {status(stage.outcome)}
                      </p>
                      <p>{stage.summary}</p>
                      {stage.signals.length ? (
                        <p className="mt-1 font-mono text-[10px] text-[var(--ls-text-tertiary)]">
                          {stage.signals.join(" · ")}
                        </p>
                      ) : null}
                    </div>
                  ))}
                </div>
              </details>
            ) : null}
            <p className="mt-2 font-mono text-[10px] text-[var(--ls-text-tertiary)]">
              {classification.classifier_version} ·{" "}
              {classification.snapshot_sha256.slice(0, 12)}
            </p>
          </section>
        ) : null}
        <AcceptanceControls key={`${detail.task.id}:${detail.acceptance?.head_sha}:${detail.acceptance?.revision}`} detail={detail} disabled={disabled} onChanged={onChanged} org={org} />
        <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
            <ShieldCheck className="size-4 text-[var(--ls-accent)]" />
            {t(" Immutable source ")}</h3>
          {sourceReady ? (
            <>
              <p className="mt-2 text-sm text-[var(--ls-text-secondary)]">
                {detail.task.source_base_ref}
              </p>
              <p className="mt-1 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">
                {detail.task.source_base_sha}
              </p>
            </>
          ) : (
            <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
              {t(detail.task.source_state === "failed" ? originGuidance.sourceFailure : originGuidance.sourcePending)}
            </p>
          )}
          {detail.task.source_state === "failed" && detail.task.state === "needs_attention" ? (
            <SourceRetry detail={detail} disabled={disabled} onChanged={onChanged} org={org} />
          ) : null}
        </section>
		<AttemptHistory attempts={detail.attempts} checkpoints={detail.publication_checkpoints ?? []} />
        {detail.attempts.some((attempt) => attempt.state === "succeeded") ? (
          <section className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
            <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
              <ClipboardCheck className="size-4 text-[var(--ls-accent)]" />
              {t(" Code review of Agent changes ")}</h3>
            {detail.linked_reviews.length ? (
              <div className="mt-3 space-y-2">
                {detail.linked_reviews.map((review) => {
                  const outcome = agentLinkedReviewOutcome(review, language);
                  return (
                    <Link
                      className="luminous-focus flex items-center justify-between gap-3 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3 py-2.5 text-sm hover:border-[var(--ls-line-strong)]"
                      href={`/${encodeURIComponent(org)}/reviews/${encodeURIComponent(review.run_id)}`}
                      key={review.run_id}
                    >
                      <span className="min-w-0">
                        <span className="block font-medium text-[var(--ls-text)]">{t("PR #")}{review.review_number} · {outcome.label}</span>
                        <span className={cn("mt-1 block text-xs leading-5", outcome.tone === "danger" ? "text-[var(--ls-danger-text)]" : outcome.tone === "success" ? "text-[var(--ls-success-text)]" : "text-[var(--ls-text-secondary)]")}>{outcome.detail}</span>
                        <span className="block truncate font-mono text-[11px] text-[var(--ls-text-tertiary)]">{review.head_sha}</span>
                      </span>
                      <ChevronRight className="size-4 shrink-0 text-[var(--ls-text-tertiary)]" />
                    </Link>
                  );
                })}
              </div>
            ) : (
              <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
                {t(" No review run is linked to this exact Agent commit yet. Check webhook delivery and the repository's Draft review policy; marking the PR ready or explicitly requesting a review can start the normal review flow when reviews are enabled. ")}<Link className="font-semibold text-[var(--ls-accent)] hover:underline" href={`/${encodeURIComponent(org)}/review-config/general?${reviewSettingsQuery}`}>{t("Repository review settings")}</Link>
              </p>
            )}
            <p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">
              {t(" A linked review is analysis evidence, not permission to merge. Branch protection and human approval remain authoritative. ")}</p>
          </section>
        ) : null}
        {isCancellableAgentTask(detail.task.state) ? (
          <TaskCancellation
            detail={detail}
            disabled={disabled}
            onChanged={onChanged}
            org={org}
          />
        ) : null}
        {!sourceReady ? null : admissionBlocked ? (
          <section className="rounded-[12px] border border-[color-mix(in_srgb,var(--ls-warning)_28%,transparent)] bg-[color-mix(in_srgb,var(--ls-warning)_7%,transparent)] p-4 text-sm leading-6 text-[var(--ls-text-secondary)]">
            <p className="font-semibold text-[var(--ls-warning-text)]">
              {classification?.decision === "rejected"
                ? t("This {resource} revision is not eligible for Agent execution.", {resource: t(originGuidance.resource)})
                : t("More trusted {resource} context is required before planning.", {resource: t(originGuidance.resource)})}
            </p>
            <p className="mt-1">
              {t(originGuidance.recovery)} {t(" Use ")}<code className="mx-1 rounded bg-[var(--ls-surface)] px-1.5 py-0.5 text-xs">
                {originGuidance.command}
              </code>
              {t(" rather than overriding this classification in the Console. ")}</p>
          </section>
        ) : (
          <PlanControls
            detail={detail}
            disabled={disabled}
            newestPlan={newestPlan}
            onChanged={onChanged}
            org={org}
          />
        )}
      </div>
    </aside>
  );
}

function SourceRetry({
  detail,
  disabled,
  onChanged,
  org,
}: {
  detail: AgentTaskDetail;
  disabled: boolean;
  onChanged: (message: string) => void;
  org: string;
}) {
  const t = useWorkflowText();
  const [pending, startTransition] = useTransition();
  const [error, setError] = useState<string>();
  const retry = () =>
    startTransition(async () => {
      setError(undefined);
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/retry-source`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ revision: detail.task.revision }),
          },
        );
        if (!response.ok) {
          const result = (await response.json()) as { error?: string };
          setError(result.error ?? t("Source retry was not accepted. Refresh the task and try again."));
          return;
        }
        onChanged(t("Recovery queued. Open Review will retry a missing provider acknowledgement first; otherwise it will re-read the source. No coding Agent has started."));
      } catch {
        setError(t("Recovery could not be queued. Check the control plane and try again."));
      }
    });
  return (
    <div className="mt-3">
      <button
        className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-xs font-semibold text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-55"
        disabled={disabled || pending}
        onClick={retry}
        type="button"
      >
        {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ShieldCheck className="size-3.5 text-[var(--ls-accent)]" />}
        {t(" Retry acknowledgement or source ")}</button>
      {error ? <p className="mt-2 text-xs text-[var(--ls-danger-text)]" role="alert">{error}</p> : null}
    </div>
  );
}

function TaskCancellation({
  detail,
  disabled,
  onChanged,
  org,
}: {
  detail: AgentTaskDetail;
  disabled: boolean;
  onChanged: (message: string) => void;
  org: string;
}) {
  const t = useWorkflowText();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string>();
  const [pending, startTransition] = useTransition();
  const cancel = () =>
    startTransition(async () => {
      setError(undefined);
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/cancel`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ revision: detail.task.revision, reason: reason.trim() }),
          },
        );
        if (!response.ok) {
          setError(await mutationError(response, t("The task could not be stopped. Refresh and try again.")));
          return;
        }
        onChanged(
          t("Agent task cancelled and its adapter lease superseded. Late results will not complete this task. If a provider write was already in flight, check the repository for a Draft PR/MR and close it if necessary."),
        );
      } catch {
        setError(t("The task could not be stopped. Check the connection and try again."));
      }
    });
  return (
    <section className="border-t border-[var(--ls-line)] pt-5">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
        <AlertTriangle className="size-4 text-[var(--ls-warning-text)]" /> {t(" Stop task ")}</h3>
      <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
        {t(" This supersedes the current lease and rejects late result callbacks. A provider write already in flight may still create a Draft PR/MR; verify the repository after stopping. Audit evidence is retained. ")}</p>
      <input
        className="luminous-focus mt-3 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]"
        disabled={disabled || pending}
        minLength={3}
        onChange={(event) => setReason(event.target.value)}
        placeholder={t("Why should this task stop?")}
        value={reason}
      />
      <button
        className="luminous-focus mt-3 inline-flex h-10 items-center gap-2 rounded-[10px] border border-[color-mix(in_srgb,var(--ls-danger)_42%,transparent)] px-4 text-sm font-semibold text-[var(--ls-danger-text)] disabled:opacity-50"
        disabled={disabled || pending || reason.trim().length < 3}
        onClick={cancel}
        type="button"
      >
        {pending ? <LoaderCircle className="size-4 animate-spin" /> : <AlertTriangle className="size-4" />}
        {t(" Stop this task ")}</button>
      {error ? <p className="mt-2 text-xs text-[var(--ls-danger-text)]" role="alert">{error}</p> : null}
    </section>
  );
}

function isCancellableAgentTask(state: AgentTask["state"]) {
  return ["received", "awaiting_approval", "execution_queued", "executing", "needs_attention"].includes(state);
}

function AttemptHistory({
  attempts,
  checkpoints,
}: {
  attempts: AgentTaskDetail["attempts"];
  checkpoints: NonNullable<AgentTaskDetail["publication_checkpoints"]>;
}) {
  const t = useWorkflowText();
  const evidence = agentAttemptEvidence(attempts, checkpoints);
  const earlier = evidence.slice(1);
  const unconfirmedEarlier = earlier.filter(({ attempt, checkpoint }) => checkpoint && !attempt.pull_request_url).length;
  return (
    <>
      <AttemptSummary attempt={evidence[0]?.attempt} checkpoint={evidence[0]?.checkpoint} />
      {earlier.length ? (
        <details className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
          <summary className="luminous-focus cursor-pointer rounded-[8px] text-xs font-semibold text-[var(--ls-text)]">
            {t(" Earlier execution attempts · ")}{earlier.length}
            {unconfirmedEarlier ? t(" · {count} with unconfirmed publication evidence", { count: unconfirmedEarlier }) : ""}
          </summary>
          <div className="mt-3 space-y-5 border-t border-[var(--ls-line)] pt-3">
            {earlier.map(({ attempt, checkpoint }) => (
              <AttemptSummary attempt={attempt} checkpoint={checkpoint} historical key={attempt.id} />
            ))}
          </div>
        </details>
      ) : null}
    </>
  );
}

function AttemptSummary({
  attempt,
  checkpoint,
  historical = false,
}: {
  attempt?: AgentTaskDetail["attempts"][number];
  checkpoint?: NonNullable<AgentTaskDetail["publication_checkpoints"]>[number];
  historical?: boolean;
}) {
  const t = useWorkflowText();
  const language = useUiLanguage();
  const status = useWorkflowStatus();
  if (!attempt) {
    return (
      <section className="border-t border-[var(--ls-line)] pt-5">
        <h3 className="text-sm font-semibold text-[var(--ls-text)]">
          {t(" Execution attempt ")}</h3>
        <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
          {t(" No execution lease has been claimed. Approval remains a governed queue handoff, not proof that a coding Agent has started. ")}</p>
      </section>
    );
  }
  const needsAttention = attempt.state === "needs_attention";
  return (
    <section className={historical ? "" : "border-t border-[var(--ls-line)] pt-5"}>
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-[var(--ls-text)]">
          {historical ? t("Attempt {number}", { number: attempt.attempt }) : t("Execution attempt")}
        </h3>
        <span
          className={cn(
            "rounded-full px-2 py-1 text-[11px] font-semibold capitalize",
            needsAttention
              ? "bg-[color-mix(in_srgb,var(--ls-warning)_16%,transparent)] text-[var(--ls-warning-text)]"
              : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
          )}
        >
          {status(attempt.state)}
        </span>
      </div>
      <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">
        {t(" attempt ")}{attempt.attempt} {t(" · task revision ")}{attempt.task_revision} {t(" · plan revision ")}{attempt.plan_revision}
      </p>
      {attempt.deadline_at ? (
        <p className="mt-1 text-xs text-[var(--ls-text-tertiary)]">
          {t(" Execution deadline · ")}{formatTime(attempt.deadline_at, language)}
        </p>
      ) : null}
      {attempt.adapter_job_id ? (
        <p className="mt-2 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">
          {t(" adapter job · ")}{attempt.adapter_job_id}
        </p>
      ) : null}
      {attempt.result_summary ? (
        <p className="mt-3 text-sm leading-5 text-[var(--ls-text-secondary)]">
          {attempt.result_summary}
        </p>
      ) : null}
      {checkpoint && !attempt.pull_request_url ? (
        <div className="mt-3 rounded-[10px] border border-[color-mix(in_srgb,var(--ls-warning)_28%,transparent)] bg-[color-mix(in_srgb,var(--ls-warning)_6%,transparent)] p-3">
          <p className="text-xs font-semibold text-[var(--ls-text)]">{t("Validated commit checkpoint · provider publication unconfirmed")}</p>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
            {t(" Attempt ")}{checkpoint.attempt_number} {t(" retained this evidence before pushing. It does not prove that a branch or Draft PR/MR exists. ")}</p>
          <p className="mt-2 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">
            {checkpoint.branch_name} · {checkpoint.head_sha}
          </p>
          <p className="mt-1 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">
            {t(" Patch SHA-256 ")}{checkpoint.patch_sha256} · {checkpoint.changed_file_count} {t(" file(s) · ")}{checkpoint.diff_bytes} {t(" diff byte(s) ")}</p>
          <ApprovedVerificationEvidence evidence={checkpoint} />
          {needsAttention ? <p className="mt-2 text-xs leading-5 text-[var(--ls-warning-text)]">{t("Inspect the provider branch and Draft before approving another attempt. This checkpoint must not be treated as a completed review.")}</p> : null}
        </div>
      ) : null}
      {attempt.pull_request_url ? (
        <div className="mt-3 rounded-[10px] border border-[color-mix(in_srgb,var(--ls-accent)_24%,transparent)] bg-[color-mix(in_srgb,var(--ls-accent)_6%,transparent)] p-3">
          <p className="text-xs font-semibold text-[var(--ls-text)]">
            {t(" Draft pull request reported by adapter ")}</p>
          <p className="mt-1 font-mono text-[11px] text-[var(--ls-text-tertiary)]">
            {attempt.branch_name} · {attempt.head_sha}
          </p>
          {attempt.patch_sha256 ? (
            <p className="mt-1 break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">
              {t(" Validated patch SHA-256 · ")}{attempt.patch_sha256} · {attempt.changed_file_count} {t(" file(s) · ")}{attempt.diff_bytes} {t(" diff byte(s) ")}</p>
          ) : null}
          <ApprovedVerificationEvidence evidence={attempt} />
          <a
            className="mt-2 inline-flex items-center gap-1 text-sm font-semibold text-[var(--ls-accent)] hover:underline"
            href={attempt.pull_request_url}
            rel="noreferrer"
            target="_blank"
          >
            {t(" Open draft pull request ")}<ExternalLink className="size-3.5" />
          </a>
          <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
            {t(" This is execution evidence only. Normal review and merge gates still apply. ")}</p>
        </div>
      ) : null}
      {attempt.error_code ? (
        <div className="mt-3 rounded-[10px] border border-[color-mix(in_srgb,var(--ls-warning)_26%,transparent)] bg-[color-mix(in_srgb,var(--ls-warning)_6%,transparent)] p-3 text-sm leading-5 text-[var(--ls-text-secondary)]">
          <p className="font-mono text-xs font-semibold text-[var(--ls-warning-text)]">
            {attempt.error_code}
          </p>
          <p className="mt-1">{attempt.error_message}</p>
          {needsAttention ? (
            <p className="mt-2">
              {attempt.error_code === "agent_executor_not_configured"
                ? t("Next step: deploy the isolated adapter, then submit and approve a new bounded plan revision.")
                : t("Next step: inspect the adapter and provider evidence, resolve the reported failure, then submit and approve a new bounded plan revision.")}
            </p>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function ApprovedVerificationEvidence({
  evidence,
}: {
  evidence: {
    verification_profile_sha256?: string;
    verification_output_sha256?: string;
    verification_output_bytes?: number;
  };
}) {
  const t = useWorkflowText();
  if (!evidence.verification_profile_sha256 || !evidence.verification_output_sha256) {
    return <p className="mt-2 text-xs leading-5 text-[var(--ls-text-tertiary)]">{t("No deployment-approved repository command is attested for this attempt.")}</p>;
  }
  return (
    <details className="mt-2 rounded-[8px] border border-[var(--ls-line)] p-2 text-xs">
      <summary className="luminous-focus cursor-pointer font-semibold text-[var(--ls-text)]">{t("Approved repository command passed · evidence")}</summary>
      <p className="mt-2 leading-5 text-[var(--ls-text-secondary)]">{t("One deployment-approved command exited successfully in the verification sandbox. This is not product acceptance or a complete CI/security/UI check.")}</p>
      <p className="mt-2 break-all font-mono leading-5 text-[var(--ls-text-tertiary)]">{t("Profile SHA-256 · ")}{evidence.verification_profile_sha256}</p>
      <p className="break-all font-mono leading-5 text-[var(--ls-text-tertiary)]">{t("Output SHA-256 · ")}{evidence.verification_output_sha256} · {evidence.verification_output_bytes ?? 0} {t(" byte(s)")}</p>
    </details>
  );
}

function PlanControls({
  detail,
  disabled,
  newestPlan,
  onChanged,
  org,
}: {
  detail: AgentTaskDetail;
  disabled: boolean;
  newestPlan?: AgentTaskDetail["plans"][number];
  onChanged: (message: string) => void;
  org: string;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const [acceptanceText,setAcceptanceText]=useState(newestPlan?.sections?.acceptance_criteria?.join("\n") ?? "");
  const [sections, setSections] = useState<AgentTaskPlanSections>({
    objective: "", scope: "", verification: "", risks: "", unknowns: "",
  });
  const [error, setError] = useState<string>();
  const [pending, startTransition] = useTransition();
  const actions = agentPlanActions(detail.task.state, newestPlan?.state);
  const historicalNotice = agentPlanHistoricalNotice(detail.task.state, newestPlan?.state);
  const canCreatePlan = actions.canCreate && detail.plan_permissions?.can_create_plan === true;
  const canApprove = actions.canApprove && detail.plan_permissions?.can_approve_plan === true;
  const createPlan = () =>
    startTransition(async () => {
      setError(undefined);
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/plans`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ sections: {...sections,source_requirements:newestPlan?.sections.source_requirements,repository_evidence:newestPlan?.sections.repository_evidence,acceptance_criteria:acceptanceText.split("\n").map(value=>value.trim()).filter(Boolean)} }),
          },
        );
        if (!response.ok) {
          setError(await mutationError(response, t("The plan was not saved. Refresh and try again.")));
          return;
        }
        setAcceptanceText("");
        setSections({ objective: "", scope: "", verification: "", risks: "", unknowns: "" });
        onChanged(t("Plan recorded. It now waits for explicit owner/admin approval."));
      } catch {
        setError(t("The plan was not saved. Check the connection and try again."));
      }
    });
  const approve = () =>
    startTransition(async () => {
      if (!newestPlan) return;
      setError(undefined);
      try {
        const response = await fetch(
          `/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/plans/${encodeURIComponent(newestPlan.id)}/approve`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ revision: newestPlan.revision }),
          },
        );
        if (!response.ok) {
          setError(await mutationError(response, t("The plan was not approved. Refresh and try again.")));
          return;
        }
        onChanged(
          t("The exact plan revision is approved and durably queued. Watch the execution attempt for adapter acceptance or a needs-attention result; approval alone does not prove the coding Agent started."),
        );
      } catch {
        setError(t("The plan was not approved. Check the connection and try again."));
      }
    });
  return (
    <section className="border-t border-[var(--ls-line)] pt-5">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-[var(--ls-text)]">
        <Sparkles className="size-4 text-[var(--ls-accent)]" /> {t(" Bounded plan ")}</h3>
      {newestPlan ? (
        <>
          {newestPlan.sections?.objective ? (
            <dl className="mt-3 grid gap-3 text-sm">
              {([
                ["objective", t("Objective")], ["scope", t("Scope and impact")],
                ["verification", t("Verification")], ["risks", t("Risks")],
                ["unknowns", t("Unknowns")],
              ] as const).map(([key, label]) => (
                <div className="rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2" key={key}>
                  <dt className="text-xs font-semibold uppercase tracking-wide text-[var(--ls-text-tertiary)]">{label}</dt>
                  <dd className="mt-1 whitespace-pre-wrap leading-6 text-[var(--ls-text-secondary)]">{newestPlan.sections[key]}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="mt-2 whitespace-pre-wrap text-sm leading-6 text-[var(--ls-text-secondary)]">{newestPlan.summary}</p>
          )}
          {newestPlan.sections?.acceptance_criteria?.length ? <ol className="mt-3 list-decimal space-y-1 pl-5 text-xs">{newestPlan.sections.acceptance_criteria.map((criterion,index)=><li key={index}>{criterion}</li>)}</ol> : null}
          {newestPlan.sections.source_requirements ? <details className="mt-3 text-xs"><summary className="cursor-pointer font-semibold">{t("Full frozen requirements")}</summary><pre className="mt-2 whitespace-pre-wrap break-words">{newestPlan.sections.source_requirements}</pre></details> : null}
          {newestPlan.sections.repository_evidence ? <details className="mt-3 text-xs"><summary className="cursor-pointer font-semibold">{t("Repository evidence at the frozen commit")}</summary><pre className="mt-2 whitespace-pre-wrap break-words">{newestPlan.sections.repository_evidence}</pre></details> : null}
          <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">
            {t(" revision ")}{newestPlan.revision} · {status(newestPlan.state)}
          </p>
          {historicalNotice ? (
            <p className="mt-2 rounded-[9px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
              {t(historicalNotice)}
            </p>
          ) : null}
          <div className="mt-3 rounded-[11px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-xs font-semibold text-[var(--ls-text-secondary)]">{t("Exact plan SHA-256")}</p>
              <CopyEvidenceButton label={t("Copy SHA")} value={newestPlan.plan_sha256} />
            </div>
            <code className="mt-2 block break-all font-mono text-[11px] leading-5 text-[var(--ls-text)]">{newestPlan.plan_sha256}</code>
          </div>
          {canApprove && detail.task.origin_kind === "issue" && newestPlan.state === "awaiting_approval" ? (
            <div className="mt-3 rounded-[11px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-xs font-semibold text-[var(--ls-text-secondary)]">{t("Approve from the Issue instead")}</p>
                <CopyEvidenceButton label={t("Copy command")} value={`@openreview approve ${newestPlan.plan_sha256}`} />
              </div>
              <code className="mt-2 block break-all font-mono text-[11px] leading-5 text-[var(--ls-text)]">@openreview approve {newestPlan.plan_sha256}</code>
              <p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">{t("Review the complete plan first. Post this on the exact Issue revision using a provider account mapped to a workspace owner or admin.")}</p>
            </div>
          ) : null}
          {canApprove ? (
            <button
              className="luminous-focus mt-3 inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-50"
              disabled={disabled || pending}
              onClick={approve}
              type="button"
            >
              <CheckCircle2 className="size-4" />
              {t(" Approve exact revision ")}</button>
          ) : null}
          {actions.canApprove && !canApprove ? (
            <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
              {t(agentPlanBlockMessage(detail.plan_permissions?.approve_block_reason, "approve", detail.task.origin_kind))}
            </p>
          ) : null}
        </>
      ) : null}
      {canCreatePlan ? (
        <div className={newestPlan ? "mt-5 border-t border-[var(--ls-line)] pt-4" : "mt-2"}>
          <p className="text-sm font-medium text-[var(--ls-text)]">
            {newestPlan ? t("Submit a new plan revision") : t("Write a bounded, reviewable plan")}
          </p>
          <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
            {newestPlan
              ? t("The new revision replaces any plan still awaiting approval. A failed execution requires a fresh owner/admin approval; it cannot silently rerun the previous plan.")
              : t("A plan is not a shell prompt and cannot start a coding Agent without separate owner/admin approval.")}
          </p>
          <div className="mt-3 grid gap-3">
            {([
              ["objective", t("Objective"), t("Describe the outcome and observed behavior…")],
              ["scope", t("Scope and impact"), t("Name the files, dependencies, boundaries, and exclusions…")],
              ["verification", t("Verification"), t("List tests and checks required before a Draft PR…")],
              ["risks", t("Risks"), t("State the rollback and safety concerns; use None with a reason if applicable…")],
              ["unknowns", t("Unknowns"), t("List open questions or explain why none remain…")],
            ] as const).map(([key, label, placeholder]) => (
              <label className="grid gap-1 text-xs font-semibold text-[var(--ls-text)]" key={key}>
                {label}
                <textarea
                  className="luminous-focus min-h-20 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] p-3 text-sm font-normal text-[var(--ls-text)]"
                  disabled={disabled || pending}
                  maxLength={4000}
                  onChange={(event) => setSections((current) => ({ ...current, [key]: event.target.value }))}
                  placeholder={placeholder}
                  value={sections[key]}
                />
              </label>
            ))}
          </div>
          <label className="mt-3 grid gap-1 text-xs font-semibold">{t("Acceptance criteria (one per line)")}<textarea className="luminous-focus min-h-20 rounded-lg border border-[var(--ls-line)] p-3 text-sm font-normal" value={acceptanceText} disabled={disabled || pending} maxLength={12000} onChange={event=>setAcceptanceText(event.target.value)} /></label>
          <p className="mt-2 text-xs text-[var(--ls-text-tertiary)]">
            {t(" Complete every section; each accepts up to 4,000 UTF-8 bytes. This exact content is frozen and hashed for approval. ")}</p>
          <button
            className="luminous-focus mt-3 inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-50"
            disabled={disabled || pending || !agentPlanSectionsValid({...sections,acceptance_criteria:acceptanceText.split("\n").map(value=>value.trim()).filter(Boolean)}) || (detail.task.workflow?.enabled === true && !acceptanceText.trim())}
            onClick={createPlan}
            type="button"
          >
            {pending ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : (
              <Sparkles className="size-4" />
            )}
            {newestPlan ? t("Submit revision for approval") : t("Save plan for approval")}
          </button>
        </div>
      ) : null}
      {actions.canCreate && !canCreatePlan ? (
        <p className="mt-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
          {t(agentPlanBlockMessage(detail.plan_permissions?.create_block_reason, "create", detail.task.origin_kind))}
        </p>
      ) : null}
      {error ? <p className="mt-2 text-xs text-[var(--ls-danger-text)]" role="alert">{error}</p> : null}
    </section>
  );
}

function agentPlanBlockMessage(reason: string | undefined, action: "create" | "approve", originKind: AgentTask["origin_kind"]): string {
  const guidance = agentTaskOriginGuidance(originKind);
  switch (reason) {
    case "reviewer_role_required": return "A workspace reviewer, admin, or owner must write the plan.";
    case "owner_admin_required": return "Only a workspace owner or admin may approve the plan.";
    case "source_not_ready": return guidance.planSource;
    case "classification_not_eligible": return guidance.planClassification;
    case "separate_approver_required": return "A different owner or admin must approve this revision, or the workspace owner can enable Agent plan self-approval in Settings → Approvals.";
    case "no_pending_plan": return "No current plan revision is awaiting approval.";
    case "execution_budget_exhausted": return "The frozen branch execution budget is exhausted. A policy edit cannot expand this task; a new request needs its own source verification and approval.";
    case "task_not_plannable": return "This task state no longer accepts a new plan.";
    default: return action === "approve"
      ? "Approval is unavailable until the current permissions and plan revision are reloaded."
      : "Plan creation is unavailable until source verification and permissions are reloaded.";
  }
}

async function mutationError(response: Response, fallback: string): Promise<string> {
  try {
    const body: unknown = await response.json();
    if (body && typeof body === "object" && "error" in body && typeof body.error === "string" && body.error.trim()) {
      return body.error;
    }
  } catch {
    // Proxy failures can be non-JSON; retain the actionable local fallback.
  }
  return fallback;
}
function ModePill({ mode }: { mode: AgentTaskPolicy["mode"] }) {
  const t = useWorkflowText();
  return (
    <span
      className={cn(
        "rounded-full px-2 py-1 text-[11px] font-semibold",
        mode === "manual"
          ? "bg-[color-mix(in_srgb,var(--ls-warning)_16%,transparent)] text-[var(--ls-warning-text)]"
          : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
      )}
    >
      {mode === "manual"
        ? t("Manual")
        : mode === "suggest"
          ? t("Reserved")
          : t("Disabled")}
    </span>
  );
}
function StatePill({ state }: { state: AgentTask["state"] }) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  return (
    <span className="rounded-full bg-[var(--ls-surface-muted)] px-2 py-1 text-[11px] font-semibold capitalize text-[var(--ls-text-secondary)]">
      {state === "completed" ? t("Draft delivered") : status(state)}
    </span>
  );
}
function RiskPill({
  risk,
}: {
  risk: AgentTaskDetail["classifications"][number]["risk_level"];
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  return (
    <span
      className={cn(
        "rounded-full px-2 py-1 text-[11px] font-semibold",
        risk === "high" || risk === "critical"
          ? "bg-[color-mix(in_srgb,var(--ls-danger)_14%,transparent)] text-[var(--ls-danger-text)]"
          : risk === "medium"
            ? "bg-[color-mix(in_srgb,var(--ls-warning)_16%,transparent)] text-[var(--ls-warning-text)]"
            : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]",
      )}
    >
      {status(risk)} {t(" risk ")}</span>
  );
}
function NoticeBanner({ notice }: { notice: Exclude<Notice, undefined> }) {
  return (
    <div
      className={cn(
        "flex gap-2 rounded-[12px] border px-4 py-3 text-sm",
        notice.tone === "error"
          ? "border-[color-mix(in_srgb,var(--ls-danger)_30%,transparent)] text-[var(--ls-danger-text)]"
          : "border-[color-mix(in_srgb,var(--ls-success)_30%,transparent)] text-[var(--ls-success-text)]",
      )}
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      {notice.message}
    </div>
  );
}

function AcceptanceControls({ detail, disabled, onChanged, org }: {
  detail: AgentTaskDetail;
  disabled: boolean;
  onChanged: (message: string) => void;
  org: string;
}) {
  const t = useWorkflowText();
  const status = useWorkflowStatus();
  const [evidence, setEvidence] = useState<Record<number, string>>({});
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string>();
  const [pending, startTransition] = useTransition();
  const acceptance = detail.acceptance;
  if (!acceptance) return null;
  const canDecide = acceptance.can_decide === true;
  const decide = (decision: "accepted" | "changes_requested") => startTransition(async () => {
    setError(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/acceptance`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            revision: acceptance.revision, head_sha: acceptance.head_sha, decision, reason,
            evidence: decision === "accepted" ? acceptance.criteria.map((_, index) => evidence[index] ?? "") : [],
          }),
        },
      );
      if (!response.ok) {
        setError(await mutationError(response, t("Acceptance changed; refresh the task.")));
        return;
      }
      setEvidence({});
      setReason("");
      onChanged(decision === "accepted"
        ? t("Requirement acceptance recorded for this exact commit.")
        : t("Changes recorded. A repair plan is prepared when the current Draft and frozen budget allow it."));
    } catch {
      setError(t("Acceptance could not be saved. Check the connection and retry."));
    }
  });
  return (
    <section className="space-y-3 rounded-xl border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
      <h3 className="text-sm font-semibold">{t("Requirement acceptance · ")}{status(acceptance.state)}</h3>
      <p className="break-all font-mono text-[11px] text-[var(--ls-text-tertiary)]">{t("Commit ")}{acceptance.head_sha}</p>
      <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">{acceptance.decision ? acceptance.reason : t(acceptance.reason ?? "")}</p>
      {acceptance.decision && acceptance.state!==acceptance.decision ? <p className="text-xs">{t("Recorded decision: ")}{status(acceptance.decision)} · {acceptance.decision_reason}{t(". Current checks must recover before it is effective.")}</p> : null}
      {acceptance.recovery_reason ? <p className="text-xs">{acceptance.recovery_reason}</p> : null}
      {acceptance.can_retry_checks ? <button className="luminous-focus rounded-lg border border-[var(--ls-line)] px-3 py-2 text-xs" disabled={disabled || pending} onClick={()=>startTransition(async()=>{
        setError(undefined);
        try {const response=await fetch(`/api/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(detail.task.id)}/retry-checks`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({revision:acceptance.revision})});if(!response.ok){setError(await mutationError(response,t("Check observation is not retryable.")));return}onChanged(t("Independent CI refresh queued. No coding Agent was started."))}catch{setError(t("Check refresh could not be queued."))}
      })}>{t("Refresh independent CI")}</button> : null}
      <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">
        {t(" Draft delivery is separate from requirement completion. Verify each criterion at this commit; owner or administrator approval is required. ")}</p>
      {acceptance.criteria.map((criterion, index) => (
        <label className="grid gap-1 text-xs" key={`${acceptance.head_sha}:${index}`}>
          <span>{index + 1}. {criterion}</span>
          {acceptance.verification_criteria?.filter(result=>result.criterion===criterion).map(result=><p key={result.criterion} className="font-normal text-[var(--ls-text-secondary)]">{t("Independent verifier: ")}{status(result.status)} · {result.evidence}</p>)}
          {canDecide ? (
            <textarea
              className="luminous-focus min-h-20 rounded-lg border border-[var(--ls-line)] p-2"
              maxLength={2000} value={evidence[index] ?? ""} disabled={disabled || pending}
              onChange={event => setEvidence(values => ({ ...values, [index]: event.target.value }))}
              placeholder={t("Test result or observation supporting this criterion")}
            />
          ) : acceptance.evidence?.[index] ? (
            <p className="font-normal text-[var(--ls-text-secondary)]">{acceptance.evidence[index]}</p>
          ) : null}
        </label>
      ))}
      {canDecide ? (
        <>
          <label className="grid gap-1 text-xs">{t("Decision reason ")}<textarea className="luminous-focus min-h-20 rounded-lg border border-[var(--ls-line)] p-2"
              maxLength={2000} value={reason} disabled={disabled || pending}
              onChange={event => setReason(event.target.value)} />
          </label>
          <div className="flex flex-wrap gap-2">
            <button className="luminous-focus rounded-lg border border-[var(--ls-line)] px-3 py-2 text-xs"
              disabled={disabled || pending || acceptance.state !== "awaiting_acceptance" || reason.trim().length < 3 || acceptance.criteria.some((_, index) => (evidence[index] ?? "").trim().length < 3)}
              onClick={() => decide("accepted")}>{t("Accept requirements")}</button>
            <button className="luminous-focus rounded-lg border border-[var(--ls-line)] px-3 py-2 text-xs"
              disabled={disabled || pending || reason.trim().length < 3}
              onClick={() => decide("changes_requested")}>{t("Request changes")}</button>
          </div>
        </>
      ) : null}
      {acceptance.remediation_task_id ? (
        <Link className="inline-flex text-xs font-semibold text-[var(--ls-accent)]"
          href={`/${encodeURIComponent(org)}/agent-work?task=${encodeURIComponent(acceptance.remediation_task_id)}`}>
          {t(" Open the automatically prepared repair plan ")}</Link>
      ) : null}
      {acceptance.state === "changes_requested" ? (
        <p className="text-xs">{t("The recorded feedback prepares a repair plan when the current Draft and budget allow it. Approve that plan; the updated commit will be verified and reviewed again.")}</p>
      ) : null}
      {error ? <p role="alert" className="text-xs text-[var(--ls-danger-text)]">{error}</p> : null}
    </section>
  );
}
