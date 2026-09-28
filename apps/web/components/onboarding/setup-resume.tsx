"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  ArrowRight,
  Check,
  CircleAlert,
  GitPullRequest,
  PartyPopper,
  RefreshCw,
  ShieldCheck,
  UsersRound,
} from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import { RuleCatalogBrowser } from "@/components/console/rule-catalog-browser";
import { InstallationRepositoryScopeEditor } from "@/components/console/installation-repository-scope-editor";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { ProviderMark } from "@/components/providers/provider-icons";
import type { ConnectionSetupStep } from "@/components/onboarding/connection-setup-wizard";
import type {
  ProviderInstallation,
  InstallationRepositoryData,
  ReviewConfigView,
  RuleCatalogData,
  WorkspaceSetupCheckpoint,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { setupRouteForCheckpoint } from "@/lib/setup-route";

type Severity = "low" | "medium" | "high" | "critical";
type LearningMode = "governed_policy" | "skipped";

type VisualStep =
  | "connect"
  | "install"
  | "repositories"
  | "scope"
  | "learning"
  | "publication"
  | "sync"
  | "review"
  | "ready";

const steps: Array<{
  key: VisualStep;
  label: string;
}> = [
  { key: "connect", label: "Connect" },
  { key: "install", label: "Install" },
  { key: "repositories", label: "Repositories" },
  { key: "scope", label: "Scope" },
  { key: "learning", label: "Learn" },
  { key: "publication", label: "Publish" },
  { key: "sync", label: "Sync" },
  { key: "review", label: "Baseline" },
  { key: "ready", label: "Ready" },
];

function visualStepFor(
  step: WorkspaceSetupCheckpoint["current_step"],
): VisualStep {
  switch (step) {
    case "connect":
      return "connect";
    case "review_scope":
    case "learning":
    case "severity":
    case "rules":
      return "review";
    case "complete":
      return "ready";
  }
}

function setupCopy(step: WorkspaceSetupCheckpoint["current_step"]) {
  switch (step) {
    case "review_scope":
      return {
        eyebrow: "Step 8 of 9 · Governance baseline · 1 of 4",
        title: "Choose the review coverage.",
        description:
          "The connection is verified. Confirm the recorded repository scope before the workspace begins reviewing pull requests.",
      };
    case "learning":
      return {
        eyebrow: "Step 8 of 9 · Governance baseline · 2 of 4",
        title: "Keep team knowledge explicit.",
        description:
          "Review suggestions can be governed by your team, without silently training on source code or historical comments.",
      };
    case "severity":
      return {
        eyebrow: "Step 8 of 9 · Governance baseline · 3 of 4",
        title: "Set the merge evidence threshold.",
        description:
          "Choose which findings may block a merge. This remains distinct from the publication threshold configured for an installation.",
      };
    case "rules":
      return {
        eyebrow: "Step 8 of 9 · Governance baseline · 4 of 4",
        title: "Finish with a governed baseline.",
        description:
          "Open Review will begin from your recorded connection and policy baseline. Rule authoring remains explicit and auditable in Policy.",
      };
    case "complete":
      return {
        eyebrow: "Step 9 of 9 · Ready",
        title: "Your review baseline is ready.",
        description:
          "This workspace now has a verified connection and a recorded governance baseline.",
      };
    default:
      return {
        eyebrow: "Workspace setup",
        title: "Finish the governance baseline.",
        description:
          "Your provider connection is recorded server-side. Setup remains resumable across refreshes and browser sessions.",
      };
  }
}

const details: Record<
  Exclude<WorkspaceSetupCheckpoint["current_step"], "connect" | "complete">,
  {
    title: string;
    description: string;
    next: WorkspaceSetupCheckpoint["current_step"];
    action: string;
  }
> = {
  review_scope: {
    title: "Confirm review coverage",
    description:
      "Continue only after the worker-synchronized inventory contains at least one repository covered by this installation scope. This proof is enforced by the control plane, not inferred from the form.",
    next: "learning",
    action: "Continue to privacy",
  },
  learning: {
    title: "Keep team learning explicit",
    description:
      "Open Review never trains on source code or silently learns from historic comments. Governed rules remain an explicit policy decision.",
    next: "severity",
    action: "Continue to threshold",
  },
  severity: {
    title: "Set the evidence threshold",
    description:
      "Choose the minimum finding severity that may block a merge. This gate is independent from the threshold used to publish feedback.",
    next: "rules",
    action: "Save threshold and continue",
  },
  rules: {
    title: "Start with governed rules",
    description:
      "Rules are created, tested, and independently approved in Policy. Finish with the deployment baseline; no inferred rule is fabricated during setup.",
    next: "complete",
    action: "Finish setup",
  },
};

export function SetupResume({
  checkpoint,
  generalConfig,
  generalConfigDetail,
  generalConfigSource,
  ruleCatalog,
  repositoryData,
  connection,
  next,
  onAdvance,
  onSaveBlockingSeverity,
  onSaveReviewBehavior,
  requestedStep,
  workspace,
}: {
  checkpoint: WorkspaceSetupCheckpoint;
  generalConfig?: ReviewConfigView;
  generalConfigDetail?: string;
  generalConfigSource: string;
  ruleCatalog: RuleCatalogData;
  repositoryData?: InstallationRepositoryData;
  connection?: Pick<
    ProviderInstallation,
    | "id"
    | "provider"
    | "verification"
    | "verification_state"
    | "repository_scope"
    | "automatic_reviews"
    | "author_scope"
    | "minimum_severity"
  >;
  next: string;
  onAdvance: (
    step: WorkspaceSetupCheckpoint["current_step"],
    revision: number,
    learningBoundary?: WorkspaceSetupCheckpoint["learning_boundary"],
  ) => Promise<WorkspaceSetupCheckpoint>;
  onSaveBlockingSeverity: (severity: Severity) => Promise<void>;
  onSaveReviewBehavior: (reviewDrafts: boolean, rereviewOnPush: boolean) => Promise<void>;
  requestedStep?: ConnectionSetupStep;
  workspace: string;
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const [severity, setSeverity] = useState<Severity>(
    severityFrom(generalConfig?.content?.minimum_blocking_severity),
  );
  const [reviewDrafts, setReviewDrafts] = useState(generalConfig?.content?.review_drafts === true);
  const [rereviewOnPush, setRereviewOnPush] = useState(generalConfig?.content?.rereview_on_push !== false);
  const [completed, setCompleted] = useState(checkpoint.current_step === "complete");
  const [completedCheckpoint, setCompletedCheckpoint] = useState<WorkspaceSetupCheckpoint>();
  const [learningMode, setLearningMode] = useState<LearningMode>(
    checkpoint.learning_boundary?.mode ?? "governed_policy",
  );
  const [reviewerExclusions, setReviewerExclusions] = useState(
    checkpoint.learning_boundary?.reviewer_exclusions.join("\n") ?? "",
  );
  const [repositoryScopeDirty, setRepositoryScopeDirty] = useState(false);
  const current = checkpoint.current_step;
  const displayStep = completed ? "complete" : current;
  const repositoryStage = displayStep === "review_scope" && requestedStep !== "scope";
  const visualStep = visualStepFor(displayStep);
  const progress = steps.findIndex((step) => step.key === visualStep);
  const copy = displayStep === "review_scope" && !repositoryStage
    ? {
        eyebrow: "Step 8 of 9 · Governance baseline · review scope",
        title: "Choose when reviews run.",
        description: "The repository boundary is recorded. Confirm Draft and new-commit behavior before moving to team context.",
      }
    : setupCopy(displayStep);
  const baselineStep =
    displayStep === "complete" || displayStep === "connect"
      ? undefined
      : displayStep;
  const detail = baselineStep
    ? repositoryStage
      ? {
          title: "Choose repositories",
          description: "Search the verified provider inventory and save an exact review scope before choosing automatic review behavior.",
          action: "Continue to review scope",
          next: "review_scope" as const,
        }
      : details[baselineStep]
    : undefined;
  useEffect(() => {
    if (!baselineStep) return;
    const expectedPath = setupRouteForCheckpoint(baselineStep, requestedStep);
    if (window.location.pathname === expectedPath) return;
    const url = new URL(window.location.href);
    url.pathname = expectedPath;
    url.searchParams.delete("step");
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }, [baselineStep, requestedStep]);
  const policyUnavailable =
    ((displayStep === "review_scope" && !repositoryStage) || displayStep === "severity") &&
    (generalConfigSource !== "live" || !generalConfig);
  const reviewScopeInventoryUnavailable = displayStep === "review_scope" &&
    (repositoryData?.source !== "live" || !repositoryData.repositories.length);
  const setupReturnTo = `/setup?tenant=${encodeURIComponent(workspace)}&next=${encodeURIComponent(next)}`;
  const repositorySelectionHref = `/${encodeURIComponent(workspace)}/connect?tab=repositories${connection ? `&installation_id=${encodeURIComponent(connection.id)}` : ""}&return_to=${encodeURIComponent(setupReturnTo)}`;

  async function continueSetup() {
    if (!detail || pending) return;
    if (repositoryStage) {
      router.push(`/setup/review-scope?tenant=${encodeURIComponent(workspace)}&next=${encodeURIComponent(next)}`);
      return;
    }
    setPending(true);
    setError(undefined);
    let policySaved = false;

    try {
      if (displayStep === "review_scope") {
        await onSaveReviewBehavior(reviewDrafts, rereviewOnPush);
        policySaved = true;
      }
      if (displayStep === "severity") {
        await onSaveBlockingSeverity(severity);
        policySaved = true;
      }
      const learningBoundary = displayStep === "learning"
        ? {
            mode: learningMode,
            reviewer_exclusions: reviewerExclusions
              .split(/[\n,]/)
              .map((subject) => subject.trim())
              .filter(Boolean),
          }
        : undefined;
      const savedCheckpoint = await onAdvance(detail.next, checkpoint.revision, learningBoundary);
      if (detail.next === "complete") {
        setCompletedCheckpoint(savedCheckpoint);
        setCompleted(true);
        return;
      }
      router.replace(
        `${setupRouteForCheckpoint(detail.next as "review_scope" | "learning" | "severity" | "rules")}?tenant=${encodeURIComponent(workspace)}&next=${encodeURIComponent(next)}`,
      );
      router.refresh();
    } catch (caught) {
      if (policySaved) router.refresh();
      setError(
        `${caught instanceof Error ? caught.message : "Setup progress could not be saved."}${policySaved ? " Review settings may already be saved; the latest checkpoint is being reloaded before retry." : ""}`,
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <LuminousPublicFrame>
      <main className="min-h-screen pb-12">
        <header className="luminous-frosted border-b border-[var(--ls-line)]">
          <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-5 sm:px-8">
            <div className="flex min-w-0 items-center gap-3">
              <ProductMark variant="luminous" />
              <span className="hidden h-5 w-px bg-[var(--ls-line-strong)] sm:block" />
              <span className="truncate text-sm text-[var(--ls-text-secondary)]">
                {workspace} <span className="text-[var(--ls-text-tertiary)]">/ setup</span>
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Link
                className="luminous-focus hidden rounded-lg px-2.5 py-1.5 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)] sm:inline-flex"
                href="/workspaces"
              >
                All workspaces
              </Link>
              <PublicThemeToggle />
            </div>
          </div>
        </header>

        <section className="mx-auto grid max-w-[1240px] gap-8 px-5 pb-8 pt-8 sm:px-8 sm:pt-12 lg:grid-cols-[288px_minmax(0,1fr)] lg:gap-12">
          <SetupContextPanel connection={connection} step={displayStep} />
          <div className="min-w-0 lg:pt-4">
          <div className="mx-auto max-w-2xl text-center lg:text-left">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">
              {copy.eyebrow}
            </p>
            <h1 className="mt-3 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)] sm:text-5xl">
              {copy.title}
            </h1>
            <p className="mx-auto mt-4 max-w-xl text-sm leading-6 text-[var(--ls-text-secondary)] sm:text-base lg:mx-0">
              {copy.description}
            </p>
          </div>

          <ol
            aria-label="Setup progress"
            className="mx-auto mt-10 flex max-w-3xl items-start justify-between gap-1 sm:mt-12"
          >
            {steps.map((step, index) => {
              const complete = index < progress;
              const active = index === progress;

              return (
                <li className="flex min-w-0 flex-1 items-center gap-1.5 last:flex-none" key={step.key}>
                  <div className="flex flex-col items-center gap-2">
                    <span
                      className={cn(
                        "grid size-7 place-items-center rounded-full border text-[11px] font-semibold sm:size-8",
                        complete
                          ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white"
                          : active
                            ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)] shadow-[0_0_0_4px_color-mix(in_srgb,var(--ls-accent)_12%,transparent)]"
                            : "border-[var(--ls-line)] bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)]",
                      )}
                    >
                      {complete ? <Check className="size-3.5" /> : index + 1}
                    </span>
                    <span
                      className={cn(
                        "hidden text-[11px] font-medium sm:block",
                        active || complete
                          ? "text-[var(--ls-text)]"
                          : "text-[var(--ls-text-tertiary)]",
                      )}
                    >
                      {step.label}
                    </span>
                  </div>
                  {index < steps.length - 1 ? (
                    <span
                      className={cn(
                        "mb-5 h-px min-w-1 flex-1 sm:mb-6",
                        complete ? "bg-[var(--ls-accent)]" : "bg-[var(--ls-line)]",
                      )}
                    />
                  ) : null}
                </li>
              );
            })}
          </ol>

          {baselineStep ? <BaselineProgress activeStep={baselineStep} /> : null}

          {detail ? (
            <section className="mx-auto mt-12 max-w-2xl rounded-3xl border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-5 shadow-[var(--ls-shadow-float)] backdrop-blur-sm sm:p-8">
              <div className="flex items-start gap-4">
                <span className="grid size-11 shrink-0 place-items-center rounded-2xl bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
                  <ShieldCheck className="size-5" />
                </span>
                <div>
                  <h2 className="text-xl font-semibold tracking-[-0.03em] text-[var(--ls-text)]">
                    {detail.title}
                  </h2>
                  <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">
                    {detail.description}
                  </p>
                  {repositoryStage && connection ? (
                    <RecordedCoverage connection={connection} workspace={workspace} />
                  ) : null}
                </div>
              </div>

              {repositoryStage && connection ? (
                <div className="mt-7 overflow-hidden rounded-[18px] border border-[var(--ls-line)]">
                  {repositoryData?.source === "live" ? (
                    <InstallationRepositoryScopeEditor
                      active={connection.verification_state === "verified"}
                      currentScope={connection.repository_scope}
                      installationID={connection.id}
                      key={`${connection.id}:${connection.repository_scope}`}
                      org={workspace}
                      onScopeDirtyChange={setRepositoryScopeDirty}
                      repositories={repositoryData.repositories}
                    />
                  ) : (
                    <p className="bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
                      {repositoryData?.detail ?? "The verified repository inventory is not available yet. Refresh after the provider worker synchronizes it; no repository list is being guessed."}
                    </p>
                  )}
                </div>
              ) : null}

              {displayStep === "review_scope" && !repositoryStage ? (
                <fieldset className="mt-7 grid gap-3 border-t border-[var(--ls-line)] pt-6">
                  <legend className="text-sm font-semibold text-[var(--ls-text)]">Automatic review behavior</legend>
                  <p className="text-xs leading-5 text-[var(--ls-text-secondary)]">These workspace defaults apply only where automatic review is enabled. Repository overrides can refine them later.</p>
                  <label className="flex cursor-pointer items-start gap-3 rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3.5">
                    <input checked={reviewDrafts} className="mt-0.5 size-4 accent-[var(--ls-accent)]" disabled={policyUnavailable} onChange={(event) => setReviewDrafts(event.target.checked)} type="checkbox" />
                    <span><span className="block text-sm font-medium text-[var(--ls-text)]">Review draft pull requests</span><span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">When off, drafts are skipped until marked ready.</span></span>
                  </label>
                  <label className="flex cursor-pointer items-start gap-3 rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3.5">
                    <input checked={rereviewOnPush} className="mt-0.5 size-4 accent-[var(--ls-accent)]" disabled={policyUnavailable} onChange={(event) => setRereviewOnPush(event.target.checked)} type="checkbox" />
                    <span><span className="block text-sm font-medium text-[var(--ls-text)]">Re-review on new commits</span><span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">A new PR head supersedes stale review work only when this is enabled.</span></span>
                  </label>
                </fieldset>
              ) : null}

              {displayStep === "severity" ? (
                <SeverityPicker
                  disabled={policyUnavailable}
                  onChange={setSeverity}
                  value={severity}
                />
              ) : null}

              {displayStep === "learning" ? (
                <LearningBoundaryPicker
                  mode={learningMode}
                  onChangeMode={setLearningMode}
                  onChangeReviewers={setReviewerExclusions}
                  reviewerExclusions={reviewerExclusions}
                />
              ) : null}

              {policyUnavailable ? (
                <Notice>
                  {generalConfigDetail ??
                    (displayStep === "review_scope"
                      ? "The review policy is unavailable, so Draft and push behavior cannot be saved."
                      : "The review policy is unavailable, so the merge threshold cannot be saved.")}
                </Notice>
              ) : null}
              {reviewScopeInventoryUnavailable ? (
                <Notice>
                  <span>
                    {repositoryData?.source === "live"
                      ? "No repository currently matches this installation scope in the synchronized inventory. If verification just finished, refresh after synchronization. Otherwise, correct the Advanced scope above and save it before continuing."
                      : "The synchronized repository inventory is unavailable. Restore the provider connection or control plane before continuing; no repository coverage is being assumed."}
                    <button className="luminous-focus ml-2 underline underline-offset-2" onClick={() => router.refresh()} type="button">Refresh inventory</button>
                  </span>
                </Notice>
              ) : null}
              {repositoryStage && repositoryScopeDirty ? (
                <Notice>Save the repository selection above and wait for read-only verification before continuing. Unsaved changes are not part of this workspace&apos;s review boundary.</Notice>
              ) : null}
              {error ? <Notice>{error}</Notice> : null}

              {displayStep === "rules" ? (
                <div className="mt-7 border-t border-[var(--ls-line)] pt-7">
                  <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
                    <div>
                      <h3 className="text-sm font-semibold text-[var(--ls-text)]">Choose an initial policy template</h3>
                      <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">Install any suitable release-pinned template as a governed draft. It remains non-executable until an independent approval, publication, and binding are recorded.</p>
                    </div>
                    <Link className="luminous-focus inline-flex w-fit items-center gap-1 rounded-lg text-xs font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]" href={`/${encodeURIComponent(workspace)}/rules?tab=recommended`}>Open full library <ArrowRight className="size-3.5" /></Link>
                  </div>
                  <RuleCatalogBrowser catalog={ruleCatalog} org={workspace} />
                </div>
              ) : null}

              {repositoryStage && connection ? (
                <Link
                  className="luminous-focus mt-4 inline-flex min-h-10 items-center gap-2 rounded-xl px-2 text-xs font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]"
                  href={repositorySelectionHref}
                >
                  Open full connection settings <ArrowRight className="size-4" />
                </Link>
              ) : null}

              <button
                className={cn(
                  "luminous-focus inline-flex min-h-11 items-center gap-2 rounded-xl px-4 py-2.5 text-sm font-semibold shadow-[var(--ls-shadow-control)] transition disabled:cursor-not-allowed disabled:opacity-55",
                  displayStep === "review_scope"
                    ? "mt-3 border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
                    : "mt-7 bg-[var(--ls-accent)] text-white hover:bg-[var(--ls-accent-hover)]",
                )}
                disabled={pending || policyUnavailable || reviewScopeInventoryUnavailable || (repositoryStage && repositoryScopeDirty)}
                onClick={continueSetup}
                type="button"
              >
                {pending ? "Saving…" : detail.action}
                <ArrowRight className="size-4" />
              </button>
            </section>
          ) : (
            <section className="mx-auto mt-12 max-w-2xl rounded-3xl border border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)] p-6 text-center shadow-[var(--ls-shadow-float)] sm:p-9">
              <span className="mx-auto grid size-12 place-items-center rounded-2xl bg-[color:color-mix(in_srgb,var(--ls-success)_14%,transparent)] text-[var(--ls-success-text)]">
                <PartyPopper className="size-6" />
              </span>
              <h2 className="mt-5 text-2xl font-semibold tracking-[-0.04em] text-[var(--ls-text)]">
                You&apos;re ready to review. 🎉
              </h2>
              <p className="mx-auto mt-3 max-w-md text-sm leading-6 text-[var(--ls-text-secondary)]">
                This workspace has an active provider connection and a recorded governance
                baseline. You can tune either safely in the console.
              </p>
              <ReadinessSnapshot snapshot={completedCheckpoint?.readiness ?? checkpoint.readiness} />
              <Link
                className="luminous-focus mt-7 inline-flex min-h-11 items-center gap-2 rounded-xl bg-[var(--ls-accent)] px-4 py-2.5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]"
                href={next}
              >
                Open workspace <ArrowRight className="size-4" />
              </Link>
            </section>
          )}
          </div>
        </section>
      </main>
    </LuminousPublicFrame>
  );
}

function ReadinessSnapshot({
  snapshot,
}: {
  snapshot?: NonNullable<WorkspaceSetupCheckpoint["readiness"]>;
}) {
  if (!snapshot) {
    return <p className="mx-auto mt-5 max-w-md text-xs leading-5 text-[var(--ls-text-tertiary)]">This workspace completed a legacy baseline without a retained readiness snapshot.</p>;
  }
  return (
    <dl className="mx-auto mt-6 grid max-w-xl gap-px overflow-hidden rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-success)_24%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_24%,transparent)] text-left sm:grid-cols-2">
      <ReadinessItem label="Connection" value={`${snapshot.provider === "github" ? "GitHub" : "GitLab"} · ${displayRepositoryScope(snapshot.repository_scope)}`} />
      <ReadinessItem label="Merge policy" value={`revision ${snapshot.general_config_revision} · ${snapshot.general_config_content_sha256.slice(0, 12)}`} mono />
      <ReadinessItem
        label="Learning boundary"
        value={
          snapshot.learning_mode === "governed_policy"
            ? "Governed policy only"
            : snapshot.learning_mode === "skipped"
              ? "Skipped"
              : "Not retained (legacy)"
        }
      />
      <ReadinessItem label="Rule baseline" value={`${snapshot.rule_set_count} governed rule set${snapshot.rule_set_count === 1 ? "" : "s"}`} />
    </dl>
  );
}

function ReadinessItem({ label, mono, value }: { label: string; mono?: boolean; value: string }) {
  return <div className="min-w-0 bg-[color:color-mix(in_srgb,var(--ls-success)_6%,var(--ls-surface))] px-3.5 py-3"><dt className="text-[10px] font-semibold uppercase tracking-[0.12em] text-[var(--ls-text-tertiary)]">{label}</dt><dd className={cn("mt-1 truncate text-xs font-medium text-[var(--ls-text)]", mono && "font-mono")}>{value}</dd></div>;
}

function LearningBoundaryPicker({
  mode,
  onChangeMode,
  onChangeReviewers,
  reviewerExclusions,
}: {
  mode: LearningMode;
  onChangeMode: (mode: LearningMode) => void;
  onChangeReviewers: (value: string) => void;
  reviewerExclusions: string;
}) {
  return (
    <fieldset className="mt-7 border-t border-[var(--ls-line)] pt-7">
      <legend className="text-sm font-semibold text-[var(--ls-text)]">Record the learning boundary</legend>
      <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
        Open Review does not train on your source code or historic review comments. This explicit choice is retained with the setup audit instead of being inferred from a toggle.
      </p>
      <div className="mt-4 grid gap-3 sm:grid-cols-2">
        <button
          aria-pressed={mode === "governed_policy"}
          className={cn(
            "luminous-focus rounded-[14px] border p-4 text-left transition",
            mode === "governed_policy"
              ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]"
              : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] hover:border-[var(--ls-line-strong)]",
          )}
          onClick={() => onChangeMode("governed_policy")}
          type="button"
        >
          <span className="block text-sm font-semibold text-[var(--ls-text)]">Use governed rules only</span>
          <span className="mt-1.5 block text-xs leading-5 text-[var(--ls-text-secondary)]">Start from approved policy and explicit feedback. No reviewer-history learning source is enabled.</span>
        </button>
        <button
          aria-pressed={mode === "skipped"}
          className={cn(
            "luminous-focus rounded-[14px] border p-4 text-left transition",
            mode === "skipped"
              ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]"
              : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)] hover:border-[var(--ls-line-strong)]",
          )}
          onClick={() => {
            onChangeMode("skipped");
            onChangeReviewers("");
          }}
          type="button"
        >
          <span className="block text-sm font-semibold text-[var(--ls-text)]">Skip team-learning setup</span>
          <span className="mt-1.5 block text-xs leading-5 text-[var(--ls-text-secondary)]">Keep the same no-training boundary and revisit only if your organization later opts into a governed source.</span>
        </button>
      </div>
      {mode === "governed_policy" ? (
        <label className="mt-4 block text-xs font-medium text-[var(--ls-text-secondary)]">
          Reviewer exclusions <span className="font-normal text-[var(--ls-text-tertiary)]">(optional, one subject per line)</span>
          <textarea
            className="luminous-focus mt-2 min-h-20 w-full resize-y rounded-[12px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2 text-sm leading-5 text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]"
            onChange={(event) => onChangeReviewers(event.target.value)}
            placeholder="casdoor-subject-a\ncasdoor-subject-b"
            value={reviewerExclusions}
          />
          <span className="mt-1.5 block text-[11px] font-normal leading-5 text-[var(--ls-text-tertiary)]">Retained as a future opt-in boundary; it does not activate learning in this deployment.</span>
        </label>
      ) : null}
    </fieldset>
  );
}

const baselineSteps: Array<{
  key: Exclude<WorkspaceSetupCheckpoint["current_step"], "connect" | "complete">;
  label: string;
  detail: string;
}> = [
  { key: "review_scope", label: "Coverage", detail: "Confirm the verified repository scope" },
  { key: "learning", label: "Boundary", detail: "Keep team context explicitly governed" },
  { key: "severity", label: "Threshold", detail: "Set merge-blocking evidence severity" },
  { key: "rules", label: "Rules", detail: "Start from approved policy drafts" },
];

function BaselineProgress({
  activeStep,
}: {
  activeStep: Exclude<WorkspaceSetupCheckpoint["current_step"], "connect" | "complete">;
}) {
  const activeIndex = baselineSteps.findIndex((step) => step.key === activeStep);
  return (
    <section
      aria-label="Governance baseline progress"
      className="mx-auto mt-8 max-w-2xl rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 sm:p-4"
    >
      <div className="flex items-center justify-between gap-4">
        <p className="text-xs font-semibold text-[var(--ls-text)]">Governance baseline</p>
        <p className="text-[11px] text-[var(--ls-text-tertiary)]">{activeIndex + 1} of {baselineSteps.length}</p>
      </div>
      <ol className="mt-3 grid gap-2 sm:grid-cols-4">
        {baselineSteps.map((step, index) => {
          const complete = index < activeIndex;
          const active = index === activeIndex;
          return (
            <li
              aria-current={active ? "step" : undefined}
              className={cn(
                "rounded-[11px] border px-3 py-2.5",
                active
                  ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]"
                  : complete
                    ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)]"
                    : "border-[var(--ls-line)] bg-[var(--ls-surface)]",
              )}
              key={step.key}
            >
              <div className="flex items-center gap-2">
                <span className={cn("grid size-4 place-items-center rounded-full text-[10px]", active ? "bg-[var(--ls-accent)] text-white" : complete ? "bg-[var(--ls-success)] text-white" : "bg-[var(--ls-line)] text-[var(--ls-text-tertiary)]")}>
                  {complete ? <Check className="size-2.5" /> : index + 1}
                </span>
                <span className={cn("text-xs font-semibold", active || complete ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)]")}>{step.label}</span>
              </div>
              <p className="mt-1.5 text-[10px] leading-4 text-[var(--ls-text-tertiary)]">{step.detail}</p>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function SetupContextPanel({
  connection,
  step,
}: {
  connection?: Pick<
    ProviderInstallation,
    | "id"
    | "provider"
    | "verification"
    | "repository_scope"
    | "automatic_reviews"
    | "minimum_severity"
  >;
  step: WorkspaceSetupCheckpoint["current_step"];
}) {
  const isSync = step === "rules" || step === "complete";
  const Icon = isSync ? RefreshCw : step === "learning" || step === "severity" ? UsersRound : GitPullRequest;

  return (
    <aside className="flex min-h-[520px] flex-col rounded-[24px] border border-[var(--ls-line)] bg-[color:color-mix(in_srgb,var(--ls-surface-raised)_88%,var(--ls-accent-soft))] p-6 shadow-[var(--ls-shadow-control)] sm:p-7">
      <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
        <Icon className={cn("size-5", isSync && "motion-safe:animate-[spin_6s_linear_infinite]")} />
      </span>
      <h2 className="mt-7 text-2xl font-semibold tracking-[-0.045em] text-[var(--ls-text)]">
        {isSync
          ? "Policy baseline & readiness"
          : step === "learning" || step === "severity"
            ? "Team learning & safety"
            : "Review scope"}
      </h2>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">
        {isSync
          ? "Provider inventory has synced. Rules remain explicit: install, approve, publish, and bind them without rewriting past review evidence."
          : step === "learning" || step === "severity"
            ? "Use explicit policy settings to tune review behavior while retaining a clear privacy and audit boundary."
            : "Start with the provider access you approved. Repository coverage and review cadence remain visible and changeable after setup."}
      </p>

      {connection ? (
        <div className="mt-7 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4">
          <div className="flex items-center gap-2.5">
            <span className="grid size-8 place-items-center rounded-[10px] bg-[var(--ls-surface-muted)]">
              <ProviderMark className="size-4" provider={connection.provider} />
            </span>
            <div className="min-w-0">
              <p className="text-xs font-semibold text-[var(--ls-text)]">
                {connection.provider === "github" ? "GitHub App" : "GitLab"} connected
              </p>
              <p className="mt-0.5 truncate font-mono text-[11px] text-[var(--ls-text-tertiary)]">
                {displayRepositoryScope(connection.repository_scope)}
              </p>
            </div>
          </div>
          {connection.verification ? (
            <p className="mt-3 border-t border-[var(--ls-line)] pt-3 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">
              Sync receipt · {connection.verification.state} · attempt {connection.verification.attempt} · {connection.verification.inventory_count} {connection.verification.inventory_count === 1 ? "repository" : "repositories"} {connection.verification.inventory_state === "partial" ? "in a partial inventory" : "in scope"}
            </p>
          ) : null}
        </div>
      ) : null}

      <div className="mt-auto space-y-3 pt-8">
        {[
          "Deployment-owned credentials",
          "Revision-bound review evidence",
          "Tenant-scoped governance",
        ].map((item) => (
          <div className="flex items-center gap-2.5 text-xs text-[var(--ls-text-secondary)]" key={item}>
            <span className="grid size-5 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <Check className="size-3" />
            </span>
            {item}
          </div>
        ))}
      </div>
    </aside>
  );
}

function RecordedCoverage({
  connection,
  workspace,
}: {
  connection: Pick<
    ProviderInstallation,
    | "id"
    | "provider"
    | "verification"
    | "repository_scope"
    | "automatic_reviews"
    | "author_scope"
    | "minimum_severity"
  >;
  workspace: string;
}) {
  return (
    <div className="mt-5">
      <dl className="grid gap-3 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 text-xs sm:grid-cols-2 xl:grid-cols-4">
        <div>
          <dt className="text-[var(--ls-text-tertiary)]">Repository scope</dt>
          <dd className="mt-1 truncate font-mono font-medium text-[var(--ls-text)]" title={displayRepositoryScope(connection.repository_scope)}>
            {displayRepositoryScope(connection.repository_scope)}
          </dd>
        </div>
        <div>
          <dt className="text-[var(--ls-text-tertiary)]">Review mode</dt>
          <dd className="mt-1 font-medium text-[var(--ls-text)]">
            {connection.automatic_reviews
              ? connection.author_scope === "mine" ? "Automatic · my PRs" : "Automatic · all authors"
              : "On demand"}
          </dd>
        </div>
        <div>
          <dt className="text-[var(--ls-text-tertiary)]">Publish threshold</dt>
          <dd className="mt-1 font-medium capitalize text-[var(--ls-text)]">
            {connection.minimum_severity}
          </dd>
        </div>
        <div>
          <dt className="text-[var(--ls-text-tertiary)]">Sync receipt</dt>
          <dd className="mt-1 font-medium text-[var(--ls-text)]">
            {connection.verification
              ? `${connection.verification.state} · ${connection.verification.inventory_count} ${connection.verification.inventory_count === 1 ? "repository" : "repositories"}${connection.verification.inventory_state === "partial" ? " · partial inventory" : ""}`
              : "Legacy / not retained"}
          </dd>
        </div>
      </dl>
      <Link
        className="luminous-focus mt-4 inline-flex items-center gap-1.5 text-xs font-semibold text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]"
        href={`/${encodeURIComponent(workspace)}/connect?tab=repositories&installation_id=${encodeURIComponent(connection.id)}`}
      >
        Choose from synchronized repositories <ArrowRight className="size-3.5" />
      </Link>
    </div>
  );
}

function displayRepositoryScope(scope: string) {
  return scope === "*/*"
    ? "All repositories authorized by this GitHub App"
    : scope;
}

function SeverityPicker({
  disabled,
  onChange,
  value,
}: {
  disabled: boolean;
  onChange: (value: Severity) => void;
  value: Severity;
}) {
  return (
    <fieldset className="mt-7">
      <legend className="text-sm font-semibold text-[var(--ls-text)]">
        Merge-blocking threshold
      </legend>
      <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">
        Only findings at or above this level may block merge when the gate is enabled.
      </p>
      <div className="mt-4 grid gap-2 sm:grid-cols-4">
        {(["low", "medium", "high", "critical"] as Severity[]).map((item) => (
          <label
            className={cn(
              "rounded-2xl border p-3 text-sm transition",
              disabled ? "cursor-not-allowed opacity-55" : "cursor-pointer",
              value === item
                ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                : "border-[var(--ls-line)] bg-[var(--ls-surface)] text-[var(--ls-text-secondary)] hover:border-[var(--ls-line-strong)]",
            )}
            key={item}
          >
            <input
              checked={value === item}
              className="sr-only"
              disabled={disabled}
              name="blocking-severity"
              onChange={() => onChange(item)}
              type="radio"
              value={item}
            />
            <span className="block font-semibold capitalize">{item}</span>
            <span className="mt-1 block text-[11px] leading-4 opacity-75">
              {severityDescription(item)}
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function Notice({ children }: { children: React.ReactNode }) {
  return (
    <p className="mt-5 flex gap-2 rounded-2xl border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] p-3 text-sm leading-6 text-[var(--ls-warning-text)]">
      <CircleAlert className="mt-0.5 size-4 shrink-0" />
      {children}
    </p>
  );
}

function severityDescription(severity: Severity) {
  switch (severity) {
    case "low":
      return "All actionable risk";
    case "medium":
      return "Balanced default";
    case "high":
      return "Substantial risk";
    case "critical":
      return "Escalation only";
  }
}

function severityFrom(value: unknown): Severity {
  return value === "low" || value === "medium" || value === "high" || value === "critical"
    ? value
    : "high";
}
