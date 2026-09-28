"use client";

import Link from "next/link";
import {
  CheckCircle2,
  CircleAlert,
  KeyRound,
  ShieldCheck,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { ProviderMark } from "@/components/providers/provider-icons";
import { GitLabAvailabilityGuidance } from "@/components/providers/gitlab-availability-guidance";
import type { DataSource, ProviderInstallation, ProviderProfile } from "@/lib/control-api";

type Provider = "github" | "gitlab";

export function ConnectionManager({
  detail,
  installations,
  gitlabDeploymentTokenAvailable,
  gitlabOAuthAvailable,
  gitlabProfile,
  gitlabProfileSource,
  org,
  source,
}: {
  detail?: string;
  installations: ProviderInstallation[];
  gitlabDeploymentTokenAvailable?: boolean;
  gitlabOAuthAvailable?: boolean;
  gitlabProfile?: ProviderProfile;
  gitlabProfileSource: DataSource;
  org: string;
  source: DataSource;
}) {
  const enabled = source === "live";
  function authorizationHref(provider: Provider, method: "oauth" | "deployment-token" = "oauth") {
    const next = `/${encodeURIComponent(org)}/connect`;
    const query = new URLSearchParams({ next, tenant: org });
    const route =
      provider === "github"
        ? "/api/setup/github/install"
        : method === "deployment-token"
          ? "/api/setup/gitlab/deployment-token"
          : "/api/setup/gitlab/authorize";
    return `${route}?${query.toString()}`;
  }

  if (source === "unavailable") {
    return (
      <PageState
        detail={detail ?? "Connection inventory could not be read. No provider authorization was started."}
        kind="unavailable"
        title="Provider control plane is temporarily unavailable"
      />
    );
  }

  if (source === "unconfigured") {
    return (
      <PageState
        action={(
          <RecoveryAction href="/workspaces" variant="primary">
            Return to workspaces
          </RecoveryAction>
        )}
        detail={detail ?? "Connect a live control plane before starting GitHub App or GitLab OAuth authorization."}
        kind="first-use-empty"
        title="Connections need a configured control plane"
      />
    );
  }

  return (
    <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-[var(--ls-text)]">
            <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <KeyRound className="size-4" />
            </span>
            Provider installations
          </div>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            Start a signed provider handoff. GitHub opens the App installation
            flow; GitLab uses OAuth or an operator-enabled deployment token.
            Repository scope and review policy are selected after a short-lived receipt.
          </p>
        </div>
        <span className="inline-flex w-fit items-center gap-1.5 rounded-full bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-success)]">
          <ShieldCheck className="size-3.5" />
          Credential-safe
        </span>
      </div>

      <div className="mt-6 grid gap-3 sm:grid-cols-2">
        {(["github", "gitlab"] as Provider[]).map((provider) => {
          const github = provider === "github";
          const hasProviderConnection = installations.some(
            (installation) => installation.provider === provider && installation.active,
          );
          return (
            <section
              className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"
              key={provider}
            >
              <div className="flex items-center gap-2.5">
                <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface)] shadow-[var(--ls-shadow-control)]">
                  <ProviderMark className="size-4" provider={provider} />
                </span>
                <div>
                  <h3 className="text-sm font-semibold text-[var(--ls-text)]">
                    {github ? "GitHub App" : "GitLab connection"}
                  </h3>
                  <p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">
                    {github
                      ? hasProviderConnection
                        ? "Install another GitHub App scope."
                        : "Choose repositories in GitHub."
                      : hasProviderConnection
                        ? "Add a non-overlapping repository scope on the configured GitLab instance."
                        : "Use OAuth or the deployment-managed connection for the configured GitLab instance."}
                  </p>
                  {!github && gitlabProfileSource === "live" && gitlabProfile ? (
                    <p className="mt-1 break-all text-xs text-[var(--ls-text-secondary)]">
                      {gitlabProfile.label} · {gitlabProfile.api_base_url}
                    </p>
                  ) : null}
                </div>
              </div>
              {enabled && !github && gitlabDeploymentTokenAvailable && gitlabOAuthAvailable ? (
                <div className="mt-4 grid gap-2">
                  <Button asChild size="sm">
                    <a href={authorizationHref("gitlab")}>Authorize GitLab OAuth</a>
                  </Button>
                  <Button asChild size="sm" variant="outline">
                    <a href={authorizationHref("gitlab", "deployment-token")}>Use deployment token</a>
                  </Button>
                </div>
              ) : enabled && !github && gitlabDeploymentTokenAvailable ? (
                <Button asChild className="mt-4 w-full" size="sm">
                  <a href={authorizationHref("gitlab", "deployment-token")}>Use deployment token</a>
                </Button>
              ) : enabled && !github && !gitlabOAuthAvailable ? (
                <div className="mt-4">
                  <Button className="w-full" disabled size="sm" type="button">
                    GitLab connection unavailable
                  </Button>
                  <GitLabAvailabilityGuidance profileSource={gitlabProfileSource} />
                </div>
              ) : enabled ? (
                <Button asChild className="mt-4 w-full" size="sm">
                  <a href={authorizationHref(provider)}>
                    {github
                      ? hasProviderConnection
                        ? "Add GitHub App scope"
                        : "Install GitHub App"
                      : hasProviderConnection
                        ? "Authorize GitLab scope"
                        : "Authorize GitLab"}
                  </a>
                </Button>
              ) : (
                <Button className="mt-4 w-full" disabled size="sm" type="button">
                  {github ? "Install GitHub App" : "Authorize GitLab"}
                </Button>
              )}
            </section>
          );
        })}
      </div>
      {source === "demo" ? (
        <p className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2 text-xs leading-5 text-[var(--ls-text-secondary)]">
          Preview data is read-only. Start GitHub App or GitLab OAuth authorization only from a live, signed-in control plane.
        </p>
      ) : !enabled ? (
        <p className="mt-4 rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-warning)_28%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,var(--ls-surface))] px-3 py-2 text-xs leading-5 text-[var(--ls-warning)]">
          The control plane is not reachable with this signed-in session. Check
          the web service CONTROL_API_URL and the assigned tenant role.
        </p>
      ) : null}
      <div className="mt-6 border-t border-[var(--ls-line)] pt-4">
        <p className="text-xs font-medium uppercase tracking-[0.12em] text-[var(--ls-text-tertiary)]">
          Registered connections
        </p>
        {installations.length === 0 ? (
          <p className="mt-3 text-sm text-[var(--ls-text-secondary)]">
            No provider installation is visible to this tenant yet.
          </p>
        ) : (
          <ul className="mt-3 grid gap-2 lg:grid-cols-2">
            {installations.map((installation) => (
              <li className="min-w-0" key={installation.id}>
                <Link
                  className="luminous-focus flex min-w-0 max-w-full items-center justify-between gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2.5 transition hover:border-[var(--ls-line-strong)] hover:bg-[var(--ls-surface)]"
                  href={`/${encodeURIComponent(org)}/connect/${encodeURIComponent(installation.id)}`}
                >
                  <div className="flex min-w-0 flex-1 items-center gap-2.5">
                    <span
                      className={
                        installation.provider === "github"
                          ? "text-[var(--ls-text)]"
                          : "rounded-md bg-white p-1"
                      }
                    >
                      <ProviderMark
                        className="size-4"
                        provider={installation.provider}
                      />
                    </span>
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-[var(--ls-text)]">
                        {installation.provider === "github"
                          ? "GitHub"
                          : "GitLab"}{" "}
                        · {installation.repository_scope}
                      </p>
                      <p className="mt-0.5 truncate text-xs text-[var(--ls-text-tertiary)]">
                        {installation.api_base_url} · {installation.automatic_reviews
                          ? installation.author_scope === "mine" ? "my PRs" : "all authors"
                          : "on demand"}
                      </p>
                    </div>
                  </div>
                  <InstallationState installation={installation} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function InstallationState({
  installation,
}: {
  installation: ProviderInstallation;
}) {
  if (!installation.active) {
    return (
      <span className="shrink-0 rounded-full bg-[var(--ls-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-text-tertiary)]">
        Inactive
      </span>
    );
  }

  if (installation.verification_state === "verified") {
    const healthState = installation.verification?.health_state;
    if (healthState && healthState !== "live") {
      return (
        <span
          className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/10 px-2.5 py-1 text-[11px] font-medium text-[var(--ls-warning-text)]"
          title={`Admission remains verified; the latest provider check is ${healthState}.`}
        >
          <CircleAlert className="size-3.5" />
          Verified · {healthState}
        </span>
      );
    }
    return (
      <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-emerald-500/10 px-2.5 py-1 text-[11px] font-medium text-[var(--ls-success-text)]">
        <CheckCircle2 className="size-3.5" />
        Verified
      </span>
    );
  }

  if (installation.verification_state === "legacy") {
    return (
      <span className="shrink-0 rounded-full bg-amber-500/10 px-2.5 py-1 text-[11px] font-medium text-[var(--ls-warning-text)]">
        Legacy eligible
      </span>
    );
  }

  if (installation.verification_state === "failed") {
    return (
      <span className="shrink-0 rounded-full bg-red-500/10 px-2.5 py-1 text-[11px] font-medium text-[var(--ls-critical-text)]">
        Verification failed
      </span>
    );
  }

  return (
    <span className="shrink-0 rounded-full bg-amber-500/10 px-2.5 py-1 text-[11px] font-medium text-[var(--ls-warning-text)]">
      {installation.verification_state === "checking"
        ? "Checking access"
        : "Awaiting verification"}
    </span>
  );
}
