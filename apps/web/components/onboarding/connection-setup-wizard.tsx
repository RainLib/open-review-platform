"use client";

import type { FormEvent } from "react";
import Link from "next/link";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  CircleAlert,
  ExternalLink,
  GitPullRequest,
  LockKeyhole,
  RefreshCw,
  UsersRound,
} from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { ProviderMark } from "@/components/providers/provider-icons";
import { GitLabAvailabilityGuidance } from "@/components/providers/gitlab-availability-guidance";
import type { DataSource, ProviderProfile } from "@/lib/control-api";
import { cn } from "@/lib/utils";

type Provider = "github" | "gitlab";
export type ConnectionSetupStep =
  | "provider"
  | "install"
  | "repositories"
  | "scope"
  | "learning"
  | "severity"
  | "sync";

export type ConnectionSetupForm = {
  repositoryScope: string;
  automaticReviews: boolean;
  authorScope: "all" | "mine";
  minimumSeverity: "low" | "medium" | "high" | "critical";
};

const connectionStepOrder: ConnectionSetupStep[] = [
  "provider",
  "install",
  "repositories",
  "scope",
  "learning",
  "severity",
  "sync",
];

type SetupProgressStep = ConnectionSetupStep | "baseline" | "ready";

const setupProgressSteps: Array<{
  key: SetupProgressStep;
  label: string;
}> = [
  { key: "provider", label: "Connect" },
  { key: "install", label: "Install" },
  { key: "repositories", label: "Repositories" },
  { key: "scope", label: "Scope" },
  { key: "learning", label: "Learn" },
  { key: "severity", label: "Publish" },
  { key: "sync", label: "Sync" },
  { key: "baseline", label: "Baseline" },
  { key: "ready", label: "Ready" },
];

const inputClassName =
  "luminous-focus h-11 w-full rounded-[12px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none transition placeholder:text-[var(--ls-text-tertiary)] focus:border-[var(--ls-accent)] focus:ring-4 focus:ring-[color:color-mix(in_srgb,var(--ls-accent)_12%,transparent)] disabled:cursor-not-allowed disabled:opacity-55";

export function ConnectionSetupWizard({
  canReachControlPlane,
  controlPlaneDetail,
  form,
  githubInstallURL,
  githubExistingInstallationURL,
  gitlabAuthorizeURL,
  gitlabDeploymentTokenURL,
  currentStep,
  gitlabProfile,
  gitlabProfileDetail,
  gitlabProfileSource,
  message,
  notice,
  onProviderChange,
  onStepChange,
  onSubmit,
  pending,
  provider,
  providerAuthorizationReady,
  providerAuthorScopedAvailable,
  repositoryMode,
  setForm,
  setRepositoryMode,
  workspace,
}: {
  canReachControlPlane: boolean;
  controlPlaneDetail?: string;
  form: ConnectionSetupForm;
  githubInstallURL?: string;
  githubExistingInstallationURL?: string;
  gitlabAuthorizeURL?: string;
  gitlabDeploymentTokenURL?: string;
  currentStep?: ConnectionSetupStep;
  gitlabProfile?: ProviderProfile;
  gitlabProfileDetail?: string;
  gitlabProfileSource: DataSource;
  message?: string;
  notice?: string;
  onProviderChange: (provider: Provider) => void;
  onStepChange?: (step: ConnectionSetupStep) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  pending: boolean;
  provider: Provider;
  providerAuthorizationReady: boolean;
  providerAuthorScopedAvailable: boolean;
  repositoryMode: "all" | "selected";
  setForm: React.Dispatch<React.SetStateAction<ConnectionSetupForm>>;
  setRepositoryMode: React.Dispatch<React.SetStateAction<"all" | "selected">>;
  workspace: string;
}) {
  // The server's signed provider receipt remains the authorization boundary.
  // The parent owns the presentation step so URL back/forward and draft
  // restoration cannot leave the visible panel behind its route.
  const step = providerAuthorizationReady ? (currentStep ?? "install") : "provider";
  const currentStepIndex = connectionStepOrder.indexOf(step);
  const setupNotice = noticeMessage(notice);

  function move(nextStep: ConnectionSetupStep) {
    onStepChange?.(nextStep);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function selectProvider(nextProvider: Provider) {
    onProviderChange(nextProvider);
  }

  return (
    <LuminousPublicFrame>
      <main className="min-h-screen pb-6 selection:bg-[var(--ls-accent)] selection:text-white">
        <header className="luminous-frosted border-b border-[var(--ls-line)]">
          <div className="mx-auto flex h-16 max-w-[1480px] items-center justify-between px-5 sm:px-8">
            <div className="flex min-w-0 items-center gap-3">
              <ProductMark variant="luminous" />
              <span className="hidden h-5 w-px bg-[var(--ls-line-strong)] sm:block" />
              <span className="hidden truncate text-sm text-[var(--ls-text-secondary)] sm:block">
                {workspace} <span className="text-[var(--ls-text-tertiary)]">/ setup</span>
              </span>
            </div>
            <div className="flex items-center gap-2">
              <Link
                className="luminous-focus hidden rounded-[9px] px-2.5 py-1.5 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)] sm:inline-flex"
                href="/workspaces"
              >
                All workspaces
              </Link>
              <PublicThemeToggle />
              <span className="grid size-8 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-[11px] font-semibold text-[var(--ls-accent)]">
                OR
              </span>
            </div>
          </div>
        </header>

        <div className="mx-auto max-w-[1480px] px-5 py-6 sm:px-8 sm:py-8">
          <SetupProgress currentStepIndex={currentStepIndex} />
          <div className="mt-7 grid gap-8 lg:grid-cols-[270px_minmax(0,1fr)] lg:gap-12">
            <WizardRail provider={provider} step={step} />
            <form className="min-w-0" onSubmit={onSubmit}>
              {!canReachControlPlane ? (
                <Notice>
                  {controlPlaneDetail ??
                    "The control plane cannot be reached, so this connection cannot be verified yet."}
                </Notice>
              ) : null}
              {message ? <Notice>{message}</Notice> : null}
              {setupNotice ? <Notice>{setupNotice}</Notice> : null}

              {step === "provider" ? (
                <ProviderStep
                  githubInstallURL={githubInstallURL}
                  githubExistingInstallationURL={githubExistingInstallationURL}
                  gitlabAuthorizeURL={gitlabAuthorizeURL}
                  gitlabDeploymentTokenURL={gitlabDeploymentTokenURL}
                  gitlabProfile={gitlabProfile}
                  gitlabProfileDetail={gitlabProfileDetail}
                  gitlabProfileSource={gitlabProfileSource}
                  onProviderChange={selectProvider}
                  provider={provider}
                />
              ) : null}
              {step === "install" ? (
                <InstallStep
                  gitlabProfile={gitlabProfile}
                  onBack={() => move("provider")}
                  onContinue={() => move("repositories")}
                  provider={provider}
                  providerAuthorizationReady={providerAuthorizationReady}
                />
              ) : null}
              {step === "repositories" ? (
                <RepositoriesStep
                  onBack={() => move("install")}
                  onContinue={() => move("scope")}
                  onModeChange={(mode) => {
                    setRepositoryMode(mode);
                    if (mode === "all") {
                      setForm((current) => ({
                        ...current,
                        repositoryScope: "*/*",
                      }));
                    }
                  }}
                  onScopeChange={(repositoryScope) =>
                    setForm((current) => ({ ...current, repositoryScope }))
                  }
                  provider={provider}
                  repositoryMode={repositoryMode}
                  repositoryScope={form.repositoryScope}
                />
              ) : null}
              {step === "scope" ? (
                <ScopeStep
                  automaticReviews={form.automaticReviews}
                  authorScope={form.authorScope}
                  authorScopedAvailable={providerAuthorScopedAvailable}
                  onBack={() => move("repositories")}
                  onChange={(automaticReviews) =>
                    setForm((current) => ({ ...current, automaticReviews }))
                  }
                  onAuthorScopeChange={(authorScope) =>
                    setForm((current) => ({ ...current, authorScope }))
                  }
                  onContinue={() => move("learning")}
                />
              ) : null}
              {step === "learning" ? (
                <LearningStep
                  onBack={() => move("scope")}
                  onContinue={() => move("severity")}
                />
              ) : null}
              {step === "severity" ? (
                <SeverityStep
                  minimumSeverity={form.minimumSeverity}
                  onBack={() => move("learning")}
                  onChange={(minimumSeverity) =>
                    setForm((current) => ({ ...current, minimumSeverity }))
                  }
                  onContinue={() => move("sync")}
                />
              ) : null}
              {step === "sync" ? (
                <SyncStep
                  disabled={!canReachControlPlane || pending || !providerAuthorizationReady}
                  form={form}
                  onBack={() => move("severity")}
                  pending={pending}
                  provider={provider}
                  providerAuthorizationReady={providerAuthorizationReady}
                />
              ) : null}
            </form>
          </div>
        </div>
      </main>
    </LuminousPublicFrame>
  );
}

function noticeMessage(notice: string | undefined) {
  switch (notice) {
    case "github_installation_returned":
      return "GitHub returned to this workspace. Its installation identity is held only in the signed server-side authorization receipt; choose a review scope to continue.";
    case "github_existing_installation_returned":
      return "GitHub access to the existing App installation was verified. The selected installation is held only in a short-lived signed receipt; choose a review scope to continue.";
    case "gitlab_authorization_returned":
      return "GitLab authorization completed. The execution credential is encrypted by the control plane; this browser retains only a short-lived signed receipt. Choose a review scope to continue.";
    case "gitlab_deployment_token_ready":
      return "The deployment-managed GitLab credential was selected. Its token and endpoint remain outside the browser; choose a repository scope to continue, then Open Review will verify read-only access.";
    case "gitlab_deployment_token_unavailable":
      return "A deployment-managed GitLab token is not enabled for this workspace. Set GITLAB_DEPLOYMENT_TOKEN_CONFIGURED=true only on control-api after mounting GITLAB_TOKEN on provider-call workers, then retry.";
    case "github_handoff_unavailable":
      return "The GitHub App handoff is not configured for this deployment. Set both GITHUB_APP_INSTALL_URL and OPEN_REVIEW_GITHUB_INSTALL_STATE_SECRET, then start this step again.";
    case "github_user_authorization_unavailable":
      return "GitHub installation identity verification is not configured. Set GITHUB_OAUTH_CLIENT_ID and GITHUB_OAUTH_CLIENT_SECRET, then register the exact GitHub App redirect URI /api/setup/github/authorize/complete before retrying.";
    case "github_existing_installation_unavailable":
      return "Existing GitHub App installation binding is not configured. Set GITHUB_APP_ID with the GitHub OAuth client credentials, then retry.";
    case "github_existing_installation_not_authorized":
      return "Open Review could not identify exactly one installation of this GitHub App that the authorized user can access. Select the intended organization in GitHub, then retry.";
    case "github_existing_installation_workspace_unavailable":
      return "Open Review could not confirm administrator access to this workspace before requesting GitHub installation verification. Refresh workspace access or ask an owner to grant the required role.";
    case "github_user_authorization_not_authorized":
      return "The GitHub user authorization did not prove access to this App installation. Sign in with an organization owner or installation administrator, then start the connection again.";
    case "github_user_authorization_workspace_unavailable":
      return "Open Review could not confirm administrator access to this workspace after GitHub returned. Refresh workspace access or ask an owner to grant the required role before retrying.";
    case "github_user_authorization_failed":
      return "GitHub user authorization could not be completed. Confirm the App client secret and exact redirect URI, then start the connection again.";
    case "gitlab_authorization_unavailable":
      return "GitLab OAuth is not configured for this deployment. Set GITLAB_OAUTH_CLIENT_ID, GITLAB_OAUTH_CLIENT_SECRET, and the provider authorization state secret, then try again.";
    case "gitlab_authorization_failed":
      return "GitLab did not return a usable authorization. The deployment has not saved a provider identity; start the GitLab authorization again.";
    case "gitlab_authorization_workspace_unavailable":
      return "Open Review could not confirm administrator access to this workspace before starting or completing GitLab authorization. Return to Workspaces, refresh access, or ask a workspace owner for the required role.";
    case "github_installation_workspace_unavailable":
      return "Open Review could not confirm administrator access to this workspace before starting or completing the GitHub App handoff. Return to Workspaces, refresh access, or ask a workspace owner to grant the required role.";
    default:
      return undefined;
  }
}

function SetupProgress({ currentStepIndex }: { currentStepIndex: number }) {
  return (
    <ol aria-label="Workspace setup progress" className="mx-auto flex max-w-5xl items-start">
      {setupProgressSteps.map((step, index) => {
        const complete = index < currentStepIndex;
        const active = index === currentStepIndex;
        return (
          <li className="flex min-w-0 flex-1 items-center last:flex-none" key={step.key}>
            <div className="flex min-w-0 items-center gap-2">
              <span
                className={cn(
                  "grid size-6 shrink-0 place-items-center rounded-full border text-[10px] font-semibold sm:size-7",
                  complete
                    ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white"
                    : active
                      ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)] shadow-[0_0_0_4px_color-mix(in_srgb,var(--ls-accent)_12%,transparent)]"
                      : "border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)]",
                )}
              >
                {complete ? <Check className="size-3.5" /> : index + 1}
              </span>
              <span
                className={cn(
                  "hidden whitespace-nowrap text-xs font-medium md:block",
                  active || complete
                    ? "text-[var(--ls-text)]"
                    : "text-[var(--ls-text-tertiary)]",
                )}
              >
                {step.label}
              </span>
            </div>
            {index < setupProgressSteps.length - 1 ? (
              <span
                className={cn(
                  "mx-2 h-px min-w-2 flex-1 sm:mx-3",
                  complete ? "bg-[var(--ls-accent)]" : "bg-[var(--ls-line)]",
                )}
              />
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}

function WizardRail({ provider, step }: { provider: Provider; step: ConnectionSetupStep }) {
  const content: Record<ConnectionSetupStep, { title: string; detail: string }> = {
    provider: {
      title: "Connect your Git provider",
      detail:
        "Give Open Review secure, scoped access to analyze pull requests and provide actionable feedback.",
    },
    install: {
      title: "Verify the installation",
      detail:
        "An installation identity is recorded before a worker performs its separate read-only provider check.",
    },
    repositories: {
      title: "Choose review repositories",
      detail:
        "Start with the repositories this installation is permitted to access. You can refine coverage later.",
    },
    scope: {
      title: "Set your review scope",
      detail:
        "Choose whether pull requests are reviewed automatically or only when your team asks for one.",
    },
    learning: {
      title: "Keep team context explicit",
      detail:
        "Open Review starts with no historical reviewer learning. Team standards are introduced through governed policy, not silent training.",
    },
    severity: {
      title: "Set the publication floor",
      detail:
        "Choose which severity levels deserve a comment. The merge-blocking threshold is a separate governance choice after connection verification.",
    },
    sync: {
      title: "Start from evidence",
      detail:
        "The worker will verify provider access before review admission. Setup remains safely resumable.",
    },
  };
  const current = content[step];
  const icon = step === "learning" ? UsersRound : step === "sync" ? RefreshCw : GitPullRequest;
  const Icon = icon;

  return (
    <aside className="flex min-h-[490px] flex-col rounded-[24px] border border-[var(--ls-line)] bg-[color:color-mix(in_srgb,var(--ls-surface-raised)_88%,var(--ls-accent-soft))] p-6 shadow-[var(--ls-shadow-control)] sm:p-7">
      <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
        <Icon className={cn("size-5", step === "sync" && "motion-safe:animate-[spin_6s_linear_infinite]")} />
      </span>
      <h1 className="mt-7 text-3xl font-semibold leading-[1.1] tracking-[-0.055em] text-[var(--ls-text)]">
        {current.title}
      </h1>
      <p className="mt-4 text-sm leading-6 text-[var(--ls-text-secondary)]">
        {current.detail}
      </p>
      <div className="mt-8 space-y-3">
        {[
          "Read-only verification",
          "No provider secret in browser",
          "Workspace-scoped evidence",
        ].map((item) => (
          <div className="flex items-center gap-2.5 text-xs text-[var(--ls-text-secondary)]" key={item}>
            <span className="grid size-5 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <Check className="size-3" />
            </span>
            {item}
          </div>
        ))}
      </div>
      <div className="mt-auto rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-4">
        <div className="flex items-center gap-2.5">
          <span className="grid size-8 place-items-center rounded-[10px] bg-[var(--ls-surface-muted)]">
            <ProviderMark className="size-4" provider={provider} />
          </span>
          <div>
            <p className="text-xs font-semibold text-[var(--ls-text)]">
              {provider === "github" ? "GitHub App" : "GitLab OAuth"}
            </p>
            <p className="mt-0.5 text-[11px] text-[var(--ls-text-tertiary)]">Deployment-owned connection</p>
          </div>
        </div>
      </div>
    </aside>
  );
}

function ProviderStep({
  githubInstallURL,
  githubExistingInstallationURL,
  gitlabAuthorizeURL,
  gitlabDeploymentTokenURL,
  gitlabProfile,
  gitlabProfileDetail,
  gitlabProfileSource,
  onProviderChange,
  provider,
}: {
  githubInstallURL?: string;
  githubExistingInstallationURL?: string;
  gitlabAuthorizeURL?: string;
  gitlabDeploymentTokenURL?: string;
  gitlabProfile?: ProviderProfile;
  gitlabProfileDetail?: string;
  gitlabProfileSource: DataSource;
  onProviderChange: (provider: Provider) => void;
  provider: Provider;
}) {
  return (
    <section className="mx-auto max-w-[760px]">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 1 of 9</p>
      <h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Choose your Git provider</h2>
      <p className="mt-3 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">GitHub App and deployment-configured GitLab are supported. A self-managed GitLab endpoint is selected by your deployment, never supplied by this browser.</p>

      <div className="mt-8 grid gap-3 sm:grid-cols-2">
        {(["github", "gitlab"] as Provider[]).map((item) => {
          const selected = item === provider;
          return (
            <button
              className={cn(
                "luminous-focus rounded-[18px] border p-5 text-left transition",
                selected
                  ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] shadow-[var(--ls-shadow-control)]"
                  : "border-[var(--ls-line)] bg-[var(--ls-surface)] hover:border-[var(--ls-line-strong)] hover:bg-[var(--ls-surface-muted)]",
              )}
              key={item}
              onClick={() => onProviderChange(item)}
              type="button"
            >
              <span className={cn("grid size-10 place-items-center rounded-[12px]", item === "github" ? "bg-[var(--ls-text)] text-[var(--ls-surface)]" : "bg-[var(--ls-surface-muted)]") }>
                <ProviderMark className="size-5" provider={item} />
              </span>
              <span className="mt-4 block text-sm font-semibold text-[var(--ls-text)]">{item === "github" ? "GitHub" : "GitLab"}</span>
              <span className="mt-1 block text-xs leading-5 text-[var(--ls-text-secondary)]">{item === "github" ? "Install the Open Review GitHub App" : "Use OAuth or the deployment-managed connection"}</span>
            </button>
          );
        })}
      </div>

      {provider === "gitlab" ? (
        <div className="mt-5 overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)]">
          <div className="border-b border-[var(--ls-line)] px-5 py-4">
            <h3 className="text-sm font-semibold text-[var(--ls-text)]">GitLab connection</h3>
            <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">The endpoint comes from the deployment profile. Choose OAuth for a user-bound credential, or a deployment-managed token when your operator enabled it. Tokens and custom URLs never enter this form.</p>
          </div>
          <div className="p-2">
            <div className="flex items-start gap-3 rounded-[12px] bg-[var(--ls-surface-muted)] px-3.5 py-3">
              <span className="mt-0.5 grid size-4 place-items-center rounded-full border-[5px] border-[var(--ls-accent)] bg-[var(--ls-surface)]" />
              <div className="min-w-0">
                <p className="text-sm font-medium text-[var(--ls-text)]">{gitlabProfile?.mode === "self_managed" ? "Self-managed GitLab profile" : "GitLab deployment profile"}</p>
                <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">{gitlabProfile ? `${gitlabProfile.label} · ${gitlabProfile.api_base_url}` : gitlabProfileDetail ?? "The deployment profile is unavailable to this workspace."}</p>
              </div>
            </div>
          </div>
        </div>
      ) : null}

      <div className="mt-8 flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="flex items-start gap-2 text-xs leading-5 text-[var(--ls-text-tertiary)]"><LockKeyhole className="mt-0.5 size-3.5 shrink-0" />The minimum provider permissions are verified separately before a review can run.</p>
        {provider === "github" && githubInstallURL ? (
          <div className="flex flex-col items-stretch gap-2 sm:items-end">
            <a className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href={githubInstallURL}>
              Install GitHub App <ExternalLink className="size-4" />
            </a>
            {githubExistingInstallationURL ? (
              <a className="luminous-focus inline-flex min-h-9 items-center justify-center rounded-[10px] px-3 text-xs font-medium text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" href={githubExistingInstallationURL}>
                Already installed? Verify access
              </a>
            ) : null}
          </div>
        ) : provider === "gitlab" && (gitlabAuthorizeURL || gitlabDeploymentTokenURL) ? (
          <div className="flex flex-col items-stretch gap-2 sm:items-end">
            {gitlabAuthorizeURL ? <a className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href={gitlabAuthorizeURL}>Authorize with GitLab <ExternalLink className="size-4" /></a> : null}
            {gitlabDeploymentTokenURL ? <a className={cn("luminous-focus inline-flex min-h-10 items-center justify-center gap-2 rounded-[11px] border px-4 text-sm font-semibold transition", gitlabAuthorizeURL ? "border-[var(--ls-line-strong)] bg-[var(--ls-surface)] text-[var(--ls-text)] hover:bg-[var(--ls-surface-muted)]" : "border-transparent bg-[var(--ls-accent)] text-white shadow-[var(--ls-shadow-control)] hover:bg-[var(--ls-accent-hover)]")} href={gitlabDeploymentTokenURL}>Use deployment token <LockKeyhole className="size-4" /></a> : null}
          </div>
        ) : (
          <div className="w-full sm:max-w-[360px]">
            <span className="inline-flex min-h-11 w-full items-center justify-center gap-2 rounded-[12px] bg-[var(--ls-surface-muted)] px-4 text-sm font-semibold text-[var(--ls-text-tertiary)]">
              {provider === "github" ? "GitHub App handoff unavailable" : "GitLab connection unavailable"}
            </span>
            {provider === "gitlab" ? <GitLabAvailabilityGuidance profileSource={gitlabProfileSource} /> : null}
          </div>
        )}
      </div>
    </section>
  );
}

function InstallStep({ gitlabProfile, onBack, onContinue, provider, providerAuthorizationReady }: {
  gitlabProfile?: ProviderProfile;
  onBack: () => void;
  onContinue: () => void;
  provider: Provider;
  providerAuthorizationReady: boolean;
}) {
  const providerName = provider === "github" ? "GitHub App" : "GitLab connection";
  return (
    <section className="mx-auto max-w-[760px]">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 2 of 9</p>
      <h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Confirm the installation</h2>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Open Review will not enable webhooks or CLI admission from this browser action. A worker runs the read-only verification after the setup record is submitted.</p>
      <div className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <div className="flex items-start gap-4">
          <span className="grid size-12 place-items-center rounded-[15px] bg-[var(--ls-surface-muted)]"><ProviderMark className="size-6" provider={provider} /></span>
          <div><h3 className="text-lg font-semibold text-[var(--ls-text)]">{providerName}</h3><p className="mt-1 text-sm leading-6 text-[var(--ls-text-secondary)]">The provider returned to this workspace through a signed authorization handoff. Open Review deliberately does not display or accept a provider installation identity in the browser.</p></div>
        </div>
        <p className={cn("mt-6 rounded-[12px] border px-3.5 py-3 text-xs leading-5", providerAuthorizationReady ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)] text-[var(--ls-success-text)]" : "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] text-[var(--ls-warning-text)]")}>{providerAuthorizationReady ? "Authorization receipt confirmed. Open Review still performs a separate, read-only deployment credential verification before webhooks, CLI, or reviews can run." : "No valid authorization receipt is available. Return to the previous step and start the provider authorization again."}</p>
        {provider === "gitlab" && gitlabProfile ? <p className="mt-3 rounded-[12px] bg-[var(--ls-surface-muted)] px-3.5 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">Endpoint: <span className="font-mono text-[var(--ls-text)]">{gitlabProfile.api_base_url}</span> · managed by deployment.</p> : null}
        <ol className="mt-6 space-y-3 text-sm">
          {[
            provider === "github" ? "Record GitHub App installation identity" : "Bind a deployment-managed GitLab credential and receipt",
            "Verify read-only provider access",
            "Enable webhook and CLI admission",
          ].map((label, index) => <li className="flex items-center gap-3" key={label}><span className={cn("grid size-6 place-items-center rounded-full border", index === 0 && providerAuthorizationReady ? "border-[var(--ls-success)] bg-[var(--ls-success)] text-white" : "border-[var(--ls-line-strong)] text-[var(--ls-text-tertiary)]")}>{index === 0 && providerAuthorizationReady ? <Check className="size-3.5" /> : index + 1}</span><span className={index === 0 && providerAuthorizationReady ? "text-[var(--ls-text)]" : "text-[var(--ls-text-tertiary)]"}>{label}</span></li>)}
        </ol>
      </div>
      <StepActions backLabel="Back" continueDisabled={!providerAuthorizationReady} continueLabel="Continue to repositories" onBack={onBack} onContinue={onContinue} />
    </section>
  );
}

function RepositoriesStep({ onBack, onContinue, onModeChange, onScopeChange, provider, repositoryMode, repositoryScope }: {
  onBack: () => void;
  onContinue: () => void;
  onModeChange: (mode: "all" | "selected") => void;
  onScopeChange: (scope: string) => void;
  provider: Provider;
  repositoryMode: "all" | "selected";
  repositoryScope: string;
}) {
  return (
    <section className="mx-auto max-w-[760px]">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 3 of 9</p>
      <h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Set the initial repository boundary</h2>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Declare the narrowest scope the provider worker should verify. This is not a repository selection yet: after verification, you will confirm coverage from the synchronized provider list before setup can finish.{provider === "gitlab" ? " GitLab webhooks name a project, so use exact projects or a real group/* prefix." : ""}</p>
      <div className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <fieldset><legend className="text-sm font-semibold text-[var(--ls-text)]">Repository coverage</legend><div className="mt-4 grid gap-3 sm:grid-cols-2">
          {(provider === "github" ? (["all", "selected"] as const) : (["selected"] as const)).map((mode) => <label className={cn("cursor-pointer rounded-[15px] border p-4 transition", repositoryMode === mode ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)] hover:border-[var(--ls-line-strong)]")} key={mode}><input checked={repositoryMode === mode} className="sr-only" name="repository-mode" onChange={() => onModeChange(mode)} type="radio" value={mode} /><span className="block text-sm font-semibold text-[var(--ls-text)]">{mode === "all" ? "All permitted repositories" : "Selected repositories"}</span><span className="mt-1.5 block text-xs leading-5 text-[var(--ls-text-secondary)]">{mode === "all" ? "Use the scope granted to this installation." : provider === "gitlab" ? "Enter full projects or a real group/* prefix." : "Enter a comma-separated allowlist."}</span></label>)}
        </div></fieldset>
        {repositoryMode === "all" ? (
          <div className="mt-6 rounded-[12px] bg-[var(--ls-surface-muted)] px-3.5 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
            <p className="font-medium text-[var(--ls-text)]">All repositories authorized for this GitHub App</p>
            <p className="mt-1">The worker will synchronize the provider inventory after read-only verification. You can narrow to exact repositories on the next setup page; this boundary never grants access beyond the App installation.</p>
          </div>
        ) : (
          <label className="mt-6 block space-y-2 text-xs font-medium text-[var(--ls-text)]">Selected repositories<input className={inputClassName} onChange={(event) => onScopeChange(event.target.value)} placeholder={provider === "github" ? "RainLib/api, RainLib/web" : "platform/api, platform/web or platform/*"} required value={repositoryScope} /><span className="block font-normal leading-5 text-[var(--ls-text-tertiary)]">The provider is still the upper authorization boundary. This value cannot grant access beyond it.</span></label>
        )}
      </div>
      <StepActions backLabel="Back" continueDisabled={!repositoryScope.trim()} continueLabel="Continue to scope" onBack={onBack} onContinue={onContinue} />
    </section>
  );
}

function ScopeStep({ automaticReviews, authorScope, authorScopedAvailable, onBack, onChange, onAuthorScopeChange, onContinue }: {
  automaticReviews: boolean;
  authorScope: ConnectionSetupForm["authorScope"];
  authorScopedAvailable: boolean;
  onBack: () => void;
  onChange: (value: boolean) => void;
  onAuthorScopeChange: (value: ConnectionSetupForm["authorScope"]) => void;
  onContinue: () => void;
}) {
  return (
    <section className="mx-auto max-w-[760px]">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 4 of 9</p>
      <h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Which pull requests should be reviewed?</h2>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Choose the authors and cadence for automatic review. Explicit commands retain their separate permission checks.</p>
      <fieldset className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <legend className="text-sm font-semibold text-[var(--ls-text)]">Author coverage</legend>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {(["all", "mine"] as const).map((value) => (
            <label className={cn("rounded-[16px] border p-5 transition", authorScope === value ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)]", value === "mine" && !authorScopedAvailable ? "cursor-not-allowed opacity-60" : "cursor-pointer hover:border-[var(--ls-line-strong)]")} key={value}>
              <input checked={authorScope === value} className="sr-only" disabled={value === "mine" && !authorScopedAvailable} name="review-authors" onChange={() => onAuthorScopeChange(value)} type="radio" />
              <span className="block text-sm font-semibold text-[var(--ls-text)]">{value === "all" ? "All PRs in selected repositories" : "Only PRs I authored"}</span>
              <span className="mt-2 block text-xs leading-5 text-[var(--ls-text-secondary)]">{value === "all" ? "Review every author covered by the verified installation." : "Match the immutable Git provider account ID verified during OAuth, not a display name."}</span>
            </label>
          ))}
        </div>
        {!authorScopedAvailable ? <p className="mt-3 text-xs leading-5 text-[var(--ls-text-tertiary)]">Only-my-PRs requires GitHub or GitLab OAuth. A deployment token has no user identity; reauthorize with an account to enable this choice.</p> : null}
      </fieldset>
      <fieldset className="mt-5 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <legend className="text-sm font-semibold text-[var(--ls-text)]">Review cadence</legend>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {[true, false].map((value) => (
            <label className={cn("cursor-pointer rounded-[16px] border p-5 transition", automaticReviews === value ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)] hover:border-[var(--ls-line-strong)]")} key={String(value)}>
              <input checked={automaticReviews === value} className="sr-only" name="review-cadence" onChange={() => onChange(value)} type="radio" />
              <span className="block text-sm font-semibold text-[var(--ls-text)]">{value ? "Automatic" : "Manual"}</span>
              <span className="mt-2 block text-xs leading-5 text-[var(--ls-text-secondary)]">{value ? "Review new and updated PRs in the chosen author scope." : "Run a review only through an explicit command or CLI request."}</span>
            </label>
          ))}
        </div>
        <p className="mt-5 rounded-[13px] bg-[var(--ls-surface-muted)] p-3.5 text-xs leading-5 text-[var(--ls-text-secondary)]">Draft handling and re-review after new commits are confirmed against the verified connection before this workspace begins reviewing.</p>
      </fieldset>
      <StepActions backLabel="Back" continueDisabled={authorScope === "mine" && !authorScopedAvailable} continueLabel="Continue to team context" onBack={onBack} onContinue={onContinue} />
    </section>
  );
}

function LearningStep({ onBack, onContinue }: { onBack: () => void; onContinue: () => void; }) {
  return <section className="mx-auto max-w-[760px]"><p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 5 of 9</p><h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Keep team context explicit</h2><p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Open Review starts without historical reviewer learning. Your team can later add reviewed, versioned policy and repository context without silently sending past comments or source code to a training system.</p><section className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6"><div className="flex items-start gap-3 rounded-[14px] bg-[var(--ls-surface-muted)] p-4"><LockKeyhole className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" /><div><h3 className="text-sm font-semibold text-[var(--ls-text)]">No historical reviewer ingestion</h3><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Past pull-request comments, reviewer identities, and repository source are not used as a browser-configured training feed. This is a deployment behavior, not a toggle that can be accidentally enabled here.</p></div></div><dl className="mt-6 divide-y divide-[var(--ls-line)] rounded-[15px] border border-[var(--ls-line)] text-sm"><SummaryRow label="Team standards" value="Governed policy drafts" /><SummaryRow label="Repository context" value="Explicit review configuration" /><SummaryRow label="Training boundary" value="Not enabled" /></dl><p className="mt-5 text-xs leading-5 text-[var(--ls-text-tertiary)]">The next stages set the publication threshold and then the merge threshold, then lets an administrator install release-pinned rules as drafts for independent approval.</p></section><StepActions backLabel="Back" continueLabel="Continue to publication threshold" onBack={onBack} onContinue={onContinue} /></section>;
}

function SeverityStep({ minimumSeverity, onBack, onChange, onContinue }: {
  minimumSeverity: ConnectionSetupForm["minimumSeverity"];
  onBack: () => void;
  onChange: (severity: ConnectionSetupForm["minimumSeverity"]) => void;
  onContinue: () => void;
}) {
  const levels = ["low", "medium", "high", "critical"] as const;
  return (
    <section className="mx-auto max-w-[760px]">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 6 of 9</p>
      <h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Set the publication threshold</h2>
      <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">
        Open Review will publish findings at this severity and above for this connection. The merge-blocking threshold is configured separately after provider verification.
      </p>
      <fieldset className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <legend className="sr-only">Minimum published finding severity</legend>
        <div className="grid gap-3 sm:grid-cols-4">
          {levels.map((level) => (
            <label className={cn("luminous-focus cursor-pointer rounded-[14px] border px-4 py-5 text-center transition", minimumSeverity === level ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-text)]" : "border-[var(--ls-line)] text-[var(--ls-text-secondary)] hover:border-[var(--ls-line-strong)]")} key={level}>
              <input checked={minimumSeverity === level} className="sr-only" name="minimum-published-severity" onChange={() => onChange(level)} type="radio" value={level} />
              <span className="block text-sm font-semibold capitalize">{level}</span>
            </label>
          ))}
        </div>
        <p className="mt-5 rounded-[13px] bg-[var(--ls-surface-muted)] p-3.5 text-xs leading-5 text-[var(--ls-text-secondary)]">
          Known severities below <strong className="capitalize text-[var(--ls-text)]">{minimumSeverity}</strong> are normally retained as evidence without posting inline PR/MR comments. Findings that block the separately configured merge gate remain visible so they can be fixed. Unrecognized severities also remain visible for safety. Category rules may independently hide a finding.
        </p>
      </fieldset>
      <StepActions backLabel="Back" continueLabel="Continue to verification" onBack={onBack} onContinue={onContinue} />
    </section>
  );
}

function SyncStep({ disabled, form, onBack, pending, provider, providerAuthorizationReady }: { disabled: boolean; form: ConnectionSetupForm; onBack: () => void; pending: boolean; provider: Provider; providerAuthorizationReady: boolean; }) {
  return <section className="mx-auto max-w-[760px]"><p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Step 7 of 9</p><h2 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Verify and sync your connection</h2><p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Save this scope, then wait for the deployment worker’s read-only provider check. Reviews remain blocked until that check succeeds and the workspace review baseline is complete.</p><section className="mt-8 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6"><div className="flex items-center gap-3"><span className="grid size-11 place-items-center rounded-[14px] bg-[var(--ls-surface-muted)]"><ProviderMark className="size-5" provider={provider} /></span><div><h3 className="text-sm font-semibold text-[var(--ls-text)]">{provider === "github" ? "GitHub App" : "GitLab OAuth profile"}</h3><p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">{providerAuthorizationReady ? "Signed provider authorization received" : "Provider authorization required"}</p></div></div><dl className="mt-6 divide-y divide-[var(--ls-line)] rounded-[15px] border border-[var(--ls-line)] text-sm"><SummaryRow label="Repository scope" value={form.repositoryScope} /><SummaryRow label="Review cadence" value={form.automaticReviews ? "Automatic on every PR" : "Manual / on demand"} /><SummaryRow label="Initial publish threshold" value={form.minimumSeverity} /></dl><div className={cn("mt-5 flex items-start gap-3 rounded-[14px] border p-4 text-xs leading-5", providerAuthorizationReady ? "border-[color:color-mix(in_srgb,var(--ls-success)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-success)_8%,transparent)] text-[var(--ls-success-text)]" : "border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] text-[var(--ls-warning-text)]")}><CheckCircle2 className="mt-0.5 size-4 shrink-0" />{providerAuthorizationReady ? "Provider credentials and the endpoint stay with the deployment. GitLab OAuth material is encrypted before it is persisted; this browser never sends it in the form." : "The signed provider authorization is missing or expired. Return to Connect and authorize the provider again before this scope can be saved."}</div></section><div className="mt-8 flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between"><button className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] px-3 text-sm font-medium text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" onClick={onBack} type="button"><ArrowLeft className="size-4" />Back</button><button className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-55" disabled={disabled} type="submit">{pending ? "Saving connection…" : "Save and verify connection"}<ArrowRight className="size-4" /></button></div></section>;
}

function StepActions({ backLabel, continueDisabled = false, continueLabel, onBack, onContinue }: { backLabel: string; continueDisabled?: boolean; continueLabel: string; onBack: () => void; onContinue: () => void; }) {
  return <div className="mt-8 flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between"><button className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] px-3 text-sm font-medium text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" onClick={onBack} type="button"><ArrowLeft className="size-4" />{backLabel}</button><button className="luminous-focus inline-flex min-h-11 items-center justify-center gap-2 rounded-[12px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-55" disabled={continueDisabled} onClick={onContinue} type="button">{continueLabel}<ArrowRight className="size-4" /></button></div>;
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return <div className="flex items-center justify-between gap-5 px-4 py-3"><dt className="text-xs text-[var(--ls-text-tertiary)]">{label}</dt><dd className="max-w-[60%] truncate text-right text-xs font-medium capitalize text-[var(--ls-text)]" title={value}>{value}</dd></div>;
}

function Notice({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto mb-6 flex max-w-[760px] items-start gap-3 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] p-4 text-sm leading-6 text-[var(--ls-warning-text)]"><CircleAlert className="mt-0.5 size-4 shrink-0" />{children}</div>;
}
