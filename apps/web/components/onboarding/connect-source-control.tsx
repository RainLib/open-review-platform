"use client";

import type { FormEvent, SetStateAction } from "react";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ArrowRight, Check, RefreshCw, ShieldCheck } from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import { InstallationRepositoryScopeEditor } from "@/components/console/installation-repository-scope-editor";
import {
  ConnectionSetupWizard,
  type ConnectionSetupForm,
  type ConnectionSetupStep,
} from "@/components/onboarding/connection-setup-wizard";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { SetupResume } from "@/components/onboarding/setup-resume";
import { ProviderMark } from "@/components/providers/provider-icons";
import type {
  DataSource,
  ProviderProfile,
  ProviderInstallation,
  InstallationRepositoryData,
  ReviewConfigView,
  RuleCatalogData,
  WorkspaceInitialization,
  WorkspaceSetupCheckpoint,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { recoverSetupDraft, setupDraftStorageKey, type SetupDraft } from "@/lib/setup-draft";
import { laterSetupStep, restorableRequestedStep, setupRouteForStep, setupStepFromSegment } from "@/lib/setup-route";

type Provider = "github" | "gitlab";

const providerDefaults: Record<Provider, ConnectionSetupForm> = {
  github: {
    repositoryScope: "*/*",
    automaticReviews: true,
    authorScope: "all",
    minimumSeverity: "medium",
  },
  gitlab: {
    repositoryScope: "",
    automaticReviews: true,
    authorScope: "all",
    minimumSeverity: "medium",
  },
};

function legacySetupDraftKey(workspace: string) {
  return `open-review:setup-draft:${workspace}`;
}

export function ConnectSourceControl({
  canReachControlPlane,
  controlPlaneDetail,
  githubInstallURL,
  githubExistingInstallationURL,
  gitlabAuthorizeURL,
  gitlabDeploymentTokenURL,
  gitlabProfile,
  gitlabProfileDetail,
  gitlabProfileSource,
  initialProvider,
  next,
  notice,
  initialization,
  checkpoint,
  generalConfig,
  generalConfigDetail,
  generalConfigSource,
  ruleCatalog,
  repositoryData,
  providerAuthorizationReady,
  providerAuthorScopedAvailable,
  requestedStep,
  workspace,
}: {
  canReachControlPlane: boolean;
  controlPlaneDetail?: string;
  githubInstallURL?: string;
  githubExistingInstallationURL?: string;
  gitlabAuthorizeURL?: string;
  gitlabDeploymentTokenURL?: string;
  gitlabProfile?: ProviderProfile;
  gitlabProfileDetail?: string;
  gitlabProfileSource: DataSource;
  initialProvider: Provider;
  next: string;
  notice?: string;
  initialization: WorkspaceInitialization;
  checkpoint?: WorkspaceSetupCheckpoint;
  generalConfig?: ReviewConfigView;
  generalConfigDetail?: string;
  generalConfigSource: string;
  ruleCatalog: RuleCatalogData;
  repositoryData?: InstallationRepositoryData;
  providerAuthorizationReady: boolean;
  providerAuthorScopedAvailable: boolean;
  requestedStep?: ConnectionSetupStep;
  workspace: string;
}) {
  const router = useRouter();
  const [provider, setProvider] = useState<Provider>(initialProvider);
  const [repositoryMode, setRepositoryMode] = useState<"all" | "selected">(
    initialProvider === "gitlab" ? "selected" : "all",
  );
  const [form, setForm] = useState<ConnectionSetupForm>(() => ({
    ...providerDefaults[initialProvider],
  }));
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();
  const [draftHydrated, setDraftHydrated] = useState(false);
  const [setupWizardStep, setSetupWizardStep] = useState<ConnectionSetupStep>("provider");
  const [furthestWizardStep, setFurthestWizardStep] = useState<ConnectionSetupStep>("provider");
  const setupStep = checkpoint?.current_step ?? "connect";
  const waitingForVerification = initialization.status === "verifying_connection";
  const verificationFailed = initialization.status === "connection_failed";
  const authorizationReady =
    providerAuthorizationReady && provider === initialProvider;

  function writeDraft(draft: SetupDraft) {
    if (!draftHydrated || setupStep !== "connect") return;
    window.sessionStorage.setItem(
      setupDraftStorageKey(workspace, draft.provider),
      JSON.stringify(draft),
    );
  }

  const storedDraft = useCallback((nextProvider: Provider): unknown => {
    const serialized = window.sessionStorage.getItem(
      setupDraftStorageKey(workspace, nextProvider),
    );
    if (!serialized) return undefined;
    try {
      return JSON.parse(serialized) as unknown;
    } catch {
      window.sessionStorage.removeItem(setupDraftStorageKey(workspace, nextProvider));
      return undefined;
    }
  }, [workspace]);

  function updateForm(update: SetStateAction<ConnectionSetupForm>) {
    setForm((current) => {
      const nextForm = typeof update === "function" ? update(current) : update;
      writeDraft({
        version: 5,
        provider,
        repositoryMode,
        form: nextForm,
        step: setupWizardStep,
        furthestStep: furthestWizardStep,
        authorizationReady,
      });
      return nextForm;
    });
  }

  function updateRepositoryMode(update: SetStateAction<"all" | "selected">) {
    setRepositoryMode((current) => {
      const nextRepositoryMode =
        typeof update === "function" ? update(current) : update;
      writeDraft({
        version: 5,
        provider,
        repositoryMode: nextRepositoryMode,
        form,
        step: setupWizardStep,
        furthestStep: furthestWizardStep,
        authorizationReady,
      });
      return nextRepositoryMode;
    });
  }

  function changeWizardStep(step: ConnectionSetupStep) {
    const furthestStep = laterSetupStep(furthestWizardStep, step);
    setSetupWizardStep(step);
    setFurthestWizardStep(furthestStep);
    const url = new URL(window.location.href);
    url.pathname = setupRouteForStep(step, provider);
    url.searchParams.set("provider", provider);
    url.searchParams.delete("step");
    window.history.pushState(
      window.history.state,
      "",
      `${url.pathname}${url.search}${url.hash}`,
    );
    writeDraft({ version: 5, provider, repositoryMode, form, step, furthestStep, authorizationReady });
  }

  useEffect(() => {
    let active = true;
    // Version 5 distinguishes choices made after a signed provider return
    // from pre-authorization selections. Neither draft nor URL is authority.
    queueMicrotask(() => {
      if (!active) return;
      const key = setupDraftStorageKey(workspace, initialProvider);
      if (setupStep !== "connect") {
        window.sessionStorage.removeItem(key);
        window.sessionStorage.removeItem(legacySetupDraftKey(workspace));
        setDraftHydrated(true);
        return;
      }
      try {
        // Preserve a pre-upgrade single-provider draft without letting it
        // overwrite a newer provider-scoped one.
        const legacyKey = legacySetupDraftKey(workspace);
        const legacySerialized = window.sessionStorage.getItem(legacyKey);
        if (legacySerialized) {
          try {
            const legacy = JSON.parse(legacySerialized) as unknown;
            if (legacy && typeof legacy === "object" && "provider" in legacy &&
              (legacy.provider === "github" || legacy.provider === "gitlab")) {
              const providerKey = setupDraftStorageKey(workspace, legacy.provider);
              if (!window.sessionStorage.getItem(providerKey)) {
                window.sessionStorage.setItem(providerKey, legacySerialized);
              }
            }
          } catch {
            // A corrupt old draft must not erase a valid provider-scoped one.
          }
          window.sessionStorage.removeItem(legacyKey);
        }
        const serialized = window.sessionStorage.getItem(key);
        const recovery = recoverSetupDraft(
          serialized ? JSON.parse(serialized) as unknown : undefined,
          initialProvider,
          providerAuthorizationReady,
        );
        const reachedStep = recovery.draft?.authorizationReady && providerAuthorizationReady
          ? laterSetupStep(recovery.step, recovery.draft.furthestStep ?? recovery.step)
          : recovery.step;
        const restoredStep = restorableRequestedStep(recovery.step, requestedStep, providerAuthorizationReady, reachedStep);
        setSetupWizardStep(restoredStep);
        setFurthestWizardStep(reachedStep);
        const url = new URL(window.location.href);
        const restoredPath = setupRouteForStep(restoredStep, initialProvider);
        if (url.pathname !== restoredPath || url.searchParams.has("step")) {
          url.pathname = restoredPath;
          url.searchParams.delete("step");
          window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
        }
        if (recovery.draft) {
          if (!providerAuthorizationReady) setProvider(recovery.draft.provider);
          setRepositoryMode(recovery.draft.repositoryMode);
          setForm(recovery.draft.form);
          setMessage("Restored unsaved setup selections from this browser tab.");
        } else if (serialized) {
          window.sessionStorage.removeItem(key);
        }
      } catch {
        window.sessionStorage.removeItem(key);
        window.sessionStorage.removeItem(legacySetupDraftKey(workspace));
        const fallbackStep = providerAuthorizationReady ? "install" : "provider";
        setSetupWizardStep(fallbackStep);
        setFurthestWizardStep(fallbackStep);
      } finally {
        setDraftHydrated(true);
      }
    });
    return () => {
      active = false;
    };
  }, [initialProvider, providerAuthorizationReady, requestedStep, setupStep, workspace]);

  useEffect(() => {
    if (setupStep !== "connect") return;
    const restoreHistoryStep = () => {
      const segment = window.location.pathname.split("/")[2] ?? "";
      const requested = setupStepFromSegment(segment);
      const queryProvider = new URL(window.location.href).searchParams.get("provider");
      const historyProvider = segment === "github" || segment === "gitlab"
        ? segment
        : queryProvider === "github" || queryProvider === "gitlab"
          ? queryProvider
          : provider;
      const historyAuthorized = historyProvider === initialProvider && providerAuthorizationReady;
      const recovery = recoverSetupDraft(storedDraft(historyProvider), historyProvider, historyAuthorized);
      const reachedStep = recovery.draft?.authorizationReady && historyAuthorized
        ? laterSetupStep(recovery.step, recovery.draft.furthestStep ?? recovery.step)
        : recovery.step;
      setProvider(historyProvider);
      setForm(recovery.draft?.form ?? { ...providerDefaults[historyProvider] });
      setRepositoryMode(recovery.draft?.repositoryMode ?? (historyProvider === "gitlab" ? "selected" : "all"));
      setSetupWizardStep(restorableRequestedStep(recovery.step, requested, historyAuthorized, reachedStep));
      setFurthestWizardStep(reachedStep);
    };
    window.addEventListener("popstate", restoreHistoryStep);
    return () => window.removeEventListener("popstate", restoreHistoryStep);
  }, [initialProvider, provider, providerAuthorizationReady, setupStep, storedDraft]);

  useEffect(() => {
    if (!draftHydrated || setupStep !== "connect") return;
    window.sessionStorage.setItem(
      setupDraftStorageKey(workspace, provider),
      JSON.stringify({
        version: 5,
        provider,
        repositoryMode,
        form,
        step: setupWizardStep,
        furthestStep: furthestWizardStep,
        authorizationReady,
      } satisfies SetupDraft),
    );
  }, [
    draftHydrated,
    authorizationReady,
    form,
    furthestWizardStep,
    provider,
    repositoryMode,
    setupStep,
    setupWizardStep,
    workspace,
  ]);

  async function advanceSetup(
    currentStep: WorkspaceSetupCheckpoint["current_step"],
    expectedRevision: number,
    learningBoundary?: WorkspaceSetupCheckpoint["learning_boundary"],
  ): Promise<WorkspaceSetupCheckpoint> {
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(workspace)}/setup-checkpoint`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          current_step: currentStep,
          expected_revision: expectedRevision,
          ...(learningBoundary ? { learning_boundary: learningBoundary } : {}),
        }),
      },
    );
    const payload = (await response.json().catch(() => ({}))) as WorkspaceSetupCheckpoint & {
      error?: string;
    };
    if (!response.ok) {
      throw new Error(payload.error ?? "Setup progress could not be saved.");
    }
    return payload;
  }

  async function saveGeneralPolicy(
    updates: Record<string, unknown>,
    failureMessage: string,
  ) {
    if (!generalConfig || generalConfigSource !== "live") {
      throw new Error(
        generalConfigDetail ?? "The review policy could not be loaded.",
      );
    }
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(workspace)}/review-config/general`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          scope_kind: "tenant",
          scope_ref: "",
          expected_revision: generalConfig.inherited
            ? 0
            : generalConfig.revision,
          content: {
            ...generalConfig.content,
            ...updates,
          },
        }),
      },
    );
    const payload = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    if (!response.ok) {
      throw new Error(payload.error ?? failureMessage);
    }
  }

  async function saveBlockingSeverity(
    severity: "low" | "medium" | "high" | "critical",
  ) {
    if (generalConfig?.content.minimum_blocking_severity === severity) return;
    await saveGeneralPolicy(
      { minimum_blocking_severity: severity },
      "The merge threshold could not be saved.",
    );
  }

  async function saveReviewBehavior(reviewDrafts: boolean, rereviewOnPush: boolean) {
    if (generalConfig?.content.review_drafts === reviewDrafts &&
      generalConfig.content.rereview_on_push === rereviewOnPush) return;
    await saveGeneralPolicy(
      { review_drafts: reviewDrafts, rereview_on_push: rereviewOnPush },
      "The review coverage settings could not be saved.",
    );
  }

  function selectProvider(nextProvider: Provider) {
    if (nextProvider === provider) return;
    writeDraft({
      version: 5,
      provider,
      repositoryMode,
      form,
      step: setupWizardStep,
      furthestStep: furthestWizardStep,
      authorizationReady,
    });
    const nextAuthorized = nextProvider === initialProvider && providerAuthorizationReady;
    const recovery = recoverSetupDraft(storedDraft(nextProvider), nextProvider, nextAuthorized);
    const nextForm = recovery.draft?.form ?? { ...providerDefaults[nextProvider] };
    const nextRepositoryMode = recovery.draft?.repositoryMode ?? (nextProvider === "gitlab" ? "selected" : "all");
    setProvider(nextProvider);
    setForm(nextForm);
    setRepositoryMode(nextRepositoryMode);
    setMessage(undefined);
    setSetupWizardStep(recovery.step);
    setFurthestWizardStep(recovery.draft?.authorizationReady && nextAuthorized
      ? laterSetupStep(recovery.step, recovery.draft.furthestStep ?? recovery.step)
      : recovery.step);
    const url = new URL(window.location.href);
    url.pathname = setupRouteForStep(recovery.step, nextProvider);
    url.searchParams.set("provider", nextProvider);
    url.searchParams.delete("step");
    window.history.pushState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }

  async function recordConnection(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!canReachControlPlane || pending) return;
    if (!authorizationReady) {
      setMessage(
        "Authorize the selected provider first. Open Review accepts only the signed return from GitHub or GitLab, never an identifier typed into the browser.",
      );
      return;
    }

    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(workspace)}/installations`,
        {
          body: JSON.stringify({
            provider,
            repository_scope: form.repositoryScope,
            automatic_reviews: form.automaticReviews,
            author_scope: form.authorScope,
            minimum_severity: form.minimumSeverity,
          }),
          headers: { "Content-Type": "application/json" },
          method: "POST",
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (response.status === 401) {
        // A Console session can expire while a user completes an external
        // GitHub/GitLab handoff. Keep the choices in the resumable draft and
        // restart OIDC instead of presenting an opaque save failure.
        const resume = new URLSearchParams({
          tenant: workspace,
          next,
          provider,
          step: "sync",
        });
        router.push(
          `/api/auth/login?next=${encodeURIComponent(`/setup?${resume.toString()}`)}`,
        );
        return;
      }
      if (!response.ok) {
        throw new Error(payload.error ?? "The connection was not accepted.");
      }
      // Installation creation atomically starts the review_scope checkpoint.
      // Do not issue a second state transition after the provider handoff: a
      // committed installation response may be replayed safely by the server.
      window.sessionStorage.removeItem(setupDraftStorageKey(workspace, provider));
      window.sessionStorage.removeItem(legacySetupDraftKey(workspace));
      router.replace(
        `/setup/verify?tenant=${encodeURIComponent(workspace)}&next=${encodeURIComponent(next)}`,
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The connection could not be recorded.",
      );
    } finally {
      setPending(false);
    }
  }

  async function retryVerification() {
    const connection = initialization.connection;
    if (!connection || pending) return;
    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        `/api/tenants/${encodeURIComponent(workspace)}/installations/${encodeURIComponent(connection.id)}/verification`,
        { method: "POST" },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
      };
      if (!response.ok) {
        throw new Error(
          payload.error ?? "Provider verification could not be queued.",
        );
      }
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "Provider verification could not be queued.",
      );
    } finally {
      setPending(false);
    }
  }

  if (waitingForVerification || verificationFailed) {
    return (
      <ConnectionVerification
        connection={initialization.connection}
        detail={initialization.detail}
        failed={verificationFailed}
        message={message}
        onRefresh={() => router.refresh()}
        onRetry={retryVerification}
        pending={pending}
        provider={initialization.connection?.provider}
        repositoryData={repositoryData}
        verification={initialization.connection?.verification}
        workspace={workspace}
      />
    );
  }

  if (setupStep !== "connect") {
    return (
      <SetupResume
        checkpoint={checkpoint!}
        generalConfig={generalConfig}
        generalConfigDetail={generalConfigDetail}
        generalConfigSource={generalConfigSource}
        ruleCatalog={ruleCatalog}
        repositoryData={repositoryData}
        connection={initialization.connection}
        next={next}
        onAdvance={advanceSetup}
        onSaveBlockingSeverity={saveBlockingSeverity}
        onSaveReviewBehavior={saveReviewBehavior}
        requestedStep={requestedStep}
        workspace={workspace}
      />
    );
  }

  return (
      <ConnectionSetupWizard
      canReachControlPlane={canReachControlPlane}
      controlPlaneDetail={controlPlaneDetail}
      form={form}
      githubInstallURL={githubInstallURL}
      githubExistingInstallationURL={githubExistingInstallationURL}
      gitlabAuthorizeURL={gitlabAuthorizeURL}
      gitlabDeploymentTokenURL={gitlabDeploymentTokenURL}
      currentStep={draftHydrated ? setupWizardStep : undefined}
      key={draftHydrated ? `draft-restored:${provider}` : "draft-loading"}
      gitlabProfile={gitlabProfile}
      gitlabProfileDetail={gitlabProfileDetail}
      gitlabProfileSource={gitlabProfileSource}
      message={message}
      notice={notice}
      onProviderChange={selectProvider}
      onStepChange={changeWizardStep}
      onSubmit={recordConnection}
      pending={pending}
      provider={provider}
      providerAuthorizationReady={authorizationReady}
      providerAuthorScopedAvailable={providerAuthorScopedAvailable && authorizationReady}
      repositoryMode={repositoryMode}
      setForm={updateForm}
      setRepositoryMode={updateRepositoryMode}
      workspace={workspace}
    />
  );
}

function ConnectionVerification({
  connection,
  detail,
  failed,
  message,
  onRefresh,
  onRetry,
  pending,
  provider,
  repositoryData,
  verification,
  workspace,
}: {
  connection: WorkspaceInitialization["connection"];
  detail?: string;
  failed: boolean;
  message?: string;
  onRefresh: () => void;
  onRetry: () => Promise<void>;
  pending: boolean;
  provider?: Provider;
  repositoryData?: InstallationRepositoryData;
  verification?: ProviderInstallation["verification"];
  workspace: string;
}) {
  const terminalProbe = verification?.state === "completed";
  const successfulProbe = terminalProbe && verification.health_state === "live";
  useEffect(() => {
    if (failed) return;

    // A route refresh re-reads the durable probe; it never queues another
    // provider call. Pause the read loop in background tabs and check once
    // immediately when the user returns so a completed setup can advance.
    const refreshIfVisible = () => {
      if (document.visibilityState === "visible") onRefresh();
    };
    const interval = window.setInterval(refreshIfVisible, 10_000);
    document.addEventListener("visibilitychange", refreshIfVisible);
    return () => {
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", refreshIfVisible);
    };
  }, [failed, onRefresh]);

  return (
    <LuminousPublicFrame>
      <main className="min-h-screen px-5 py-6 text-[var(--ls-text)] sm:px-8 sm:py-10">
        <section className="mx-auto grid min-h-[calc(100vh-80px)] max-w-5xl items-center gap-8 lg:grid-cols-[minmax(0,1fr)_360px]">
          <div>
            <div className="flex items-center justify-between gap-4">
              <div className="flex items-center gap-3">
                <ProductMark variant="luminous" />
                <span className="h-5 w-px bg-[var(--ls-line-strong)]" />
                <span className="text-sm text-[var(--ls-text-secondary)]">
                  Connection verification
                </span>
              </div>
              <PublicThemeToggle />
            </div>
            <p className="mt-14 text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">
              Workspace setup
            </p>
            <h1 aria-live="polite" className="mt-3 max-w-xl text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)] sm:text-5xl">
              {failed
                ? "Provider access needs attention."
                : verification?.state === "running"
                  ? "Provider verification has started."
                  : "Waiting for provider verification."}
            </h1>
            <p className="mt-5 max-w-xl text-base leading-7 text-[var(--ls-text-secondary)]">
              {detail ??
                "A read-only authorization check is queued. It requires a verification worker, and reviews remain unavailable until its result is recorded and the workspace review baseline is finished."}
            </p>
            <div className="mt-8 flex flex-wrap gap-3">
              {failed ? (
                <button
                  className="luminous-focus inline-flex items-center gap-2 rounded-xl bg-[var(--ls-accent)] px-4 py-2.5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:opacity-50"
                  disabled={pending || provider === undefined}
                  onClick={onRetry}
                  type="button"
                >
                  {pending ? "Queuing verification…" : "Retry verification"}
                  <ArrowRight className="size-4" />
                </button>
              ) : (
                <button
                  className="luminous-focus inline-flex items-center gap-2 rounded-xl border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 py-2.5 text-sm font-semibold text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)]"
                  onClick={onRefresh}
                  type="button"
                >
                  Refresh status <RefreshCw className="size-4" />
                </button>
              )}
              <Link
                className="luminous-focus inline-flex items-center rounded-xl border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 py-2.5 text-sm font-medium text-[var(--ls-text-secondary)] transition hover:border-[var(--ls-accent)] hover:text-[var(--ls-text)]"
                href="/workspaces"
              >
                Back to workspaces
              </Link>
            </div>
            {message ? (
              <p
                aria-live="polite"
                className="mt-4 text-sm leading-6 text-[var(--ls-warning)]"
              >
                {message}
              </p>
            ) : null}
            {failed && connection ? (
              <div className="mt-7 overflow-hidden rounded-[18px] border border-[var(--ls-line)]">
                {repositoryData?.source === "live" ? (
                  <InstallationRepositoryScopeEditor
                    active={connection.active}
                    currentScope={connection.repository_scope}
                    installationID={connection.id}
                    key={`${connection.id}:${connection.repository_scope}`}
                    org={workspace}
                    repositories={repositoryData.repositories}
                  />
                ) : (
                  <p className="bg-[var(--ls-surface-muted)] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]">
                    {repositoryData?.detail ?? "Repository inventory is unavailable. Restore the control plane before correcting this connection's scope."}
                  </p>
                )}
              </div>
            ) : null}
            {!failed ? (
              <p className="mt-4 max-w-xl text-xs leading-5 text-[var(--ls-text-tertiary)]">
                This task is retained on the deployment after you leave this
                page. It progresses only while a verification worker is running.
                This page checks the saved status every 10 seconds while visible
                and when you return to the tab. You can also refresh manually;
                neither action submits another verification task.
              </p>
            ) : null}
          </div>
          <aside className="rounded-3xl border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-6 shadow-[var(--ls-shadow-float)] backdrop-blur-sm">
            <div className="flex items-center gap-3">
              <span className="grid size-11 place-items-center rounded-2xl bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
                {provider ? (
                  <ProviderMark className="size-5" provider={provider} />
                ) : (
                  <ShieldCheck className="size-5" />
                )}
              </span>
              <div>
                <p className="text-sm font-semibold text-[var(--ls-text)]">
                  {provider === "github"
                    ? "GitHub App"
                    : provider === "gitlab"
                      ? "GitLab"
                      : "Provider"}{" "}
                  verification
                </p>
                <p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">
                  Deployment-owned credential
                </p>
              </div>
            </div>
            <ol className="mt-7 space-y-4 text-sm">
              <VerificationStep
                complete={terminalProbe}
                current={!failed}
                label="Run read-only provider probe"
              />
              <VerificationStep
                complete={successfulProbe}
                current={!failed}
                label="Verify read-only provider access"
              />
              <VerificationStep
                complete={false}
                current={false}
                label="Await governance baseline before admission"
              />
            </ol>
            {verification ? (
              <dl className="mt-7 space-y-3 border-t border-[var(--ls-line)] pt-5 text-xs">
                <VerificationReceiptItem label="Probe receipt" value={`${verification.state} · attempt ${verification.attempt}`} />
                <VerificationReceiptItem label="Inventory" value={`${verification.inventory_count} ${verification.inventory_state === "partial" ? "partially synchronized" : verification.inventory_state === "synchronized" ? "synchronized" : "recorded"} ${verification.inventory_count === 1 ? "repository" : "repositories"}`} />
                {verification.inventory_state === "partial" ? <p className="rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2 leading-5 text-[var(--ls-text-secondary)]">The provider has more repositories than this bounded sync could confirm, or a later page failed. Search may omit repositories. You can enter an exact repository scope and retry verification.</p> : null}
                <VerificationReceiptItem label="Permissions" value={verification.permissions.length ? verification.permissions.join(", ") : "Not retained"} />
                {verification.error_code ? <VerificationReceiptItem label="Recovery code" value={verification.error_code} /> : null}
              </dl>
            ) : null}
            <p className="mt-7 border-t border-[var(--ls-line)] pt-5 text-xs leading-5 text-[var(--ls-text-tertiary)]">
              No source code is transmitted by this check. It does not test
              provider write permission or fabricate a review result.
            </p>
          </aside>
        </section>
      </main>
    </LuminousPublicFrame>
  );
}

function VerificationReceiptItem({ label, value }: { label: string; value: string }) {
  return <div className="flex items-start justify-between gap-3"><dt className="shrink-0 text-[var(--ls-text-tertiary)]">{label}</dt><dd className="min-w-0 text-right font-mono leading-5 text-[var(--ls-text-secondary)]">{value}</dd></div>;
}

function VerificationStep({
  complete,
  current,
  label,
}: {
  complete: boolean;
  current: boolean;
  label: string;
}) {
  return (
    <li className="flex items-center gap-3">
      <span
        className={cn(
          "grid size-6 place-items-center rounded-full border",
          complete
            ? "border-[var(--ls-success)] bg-[var(--ls-success)] text-white"
            : current
              ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
              : "border-[var(--ls-line-strong)] text-[var(--ls-text-tertiary)]",
        )}
      >
        {complete ? (
          <Check className="size-3.5" />
        ) : (
          <span
            className={cn(
              "size-1.5 rounded-full",
              current
                ? "animate-pulse bg-[var(--ls-accent)]"
                : "bg-[var(--ls-text-tertiary)]",
            )}
          />
        )}
      </span>
      <span
        className={
          current
            ? "text-[var(--ls-text)]"
            : "text-[var(--ls-text-tertiary)]"
        }
      >
        {label}
      </span>
    </li>
  );
}
