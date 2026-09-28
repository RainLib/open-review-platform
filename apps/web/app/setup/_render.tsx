import { redirect } from "next/navigation";
import { cookies } from "next/headers";

import { ConnectSourceControl } from "@/components/onboarding/connect-source-control";
import type { ConnectionSetupStep } from "@/components/onboarding/connection-setup-wizard";
import {
  requireConsoleSession,
  safeInternalPath,
} from "@/lib/auth/session";
import { sessionRenewalPath } from "@/lib/auth/safe-path";
import { githubInstallHandoffAvailable } from "@/lib/auth/github-install";
import {
  getGitHubUserOAuthConfiguration,
  githubExistingInstallationHandoffAvailable,
} from "@/lib/auth/github-user-oauth";
import { getGitLabOAuthConfiguration } from "@/lib/auth/gitlab-oauth";
import {
  decodeProviderAuthorization,
  PROVIDER_AUTHORIZATION_COOKIE,
} from "@/lib/auth/provider-authorization";
import {
  getReviewConfigData,
  getRuleCatalog,
  getProviderProfiles,
  getInstallationRepositories,
  getWorkspaceInitialization,
  getWorkspaceSetupCheckpoint,
} from "@/lib/control-api";

function firstValue(value: string | string[] | undefined) {
  return typeof value === "string" ? value : undefined;
}

function workspaceSlug(value: string | undefined) {
  return value && /^[a-z0-9][a-z0-9-]{1,62}$/.test(value) ? value : undefined;
}

function wizardStep(value: string | undefined): ConnectionSetupStep | undefined {
  return value === "provider" ||
    value === "install" ||
    value === "repositories" ||
    value === "scope" ||
    value === "learning" ||
    value === "severity" ||
    value === "sync"
    ? value
    : undefined;
}

export type SetupSearchParams = {
    next?: string | string[];
    notice?: string | string[];
    provider?: string | string[];
    step?: string | string[];
    tenant?: string | string[];
};

export async function renderSetupPage(
  query: SetupSearchParams,
  routeStep?: ConnectionSetupStep,
  routeProvider?: "github" | "gitlab",
) {
  const workspace = workspaceSlug(firstValue(query.tenant));
  const requestedNext = safeInternalPath(firstValue(query.next));
  const next = workspace && requestedNext.startsWith(`/${workspace}/`)
    ? requestedNext
    : workspace
      ? `/${workspace}/home`
      : "/workspaces";
  const resumedStep = routeStep ?? wizardStep(firstValue(query.step));
  const githubInstallURL = workspace &&
    githubInstallHandoffAvailable() &&
    getGitHubUserOAuthConfiguration()
    ? `/api/setup/github/install?${new URLSearchParams({
        next,
        tenant: workspace,
      }).toString()}`
    : undefined;
  const githubExistingInstallationURL = workspace &&
    githubExistingInstallationHandoffAvailable()
    ? `/api/setup/github/authorize?${new URLSearchParams({
        next,
        tenant: workspace,
      }).toString()}`
    : undefined;
  const gitlabAuthorizeURL = workspace && getGitLabOAuthConfiguration()
    ? `/api/setup/gitlab/authorize?${new URLSearchParams({
        next,
        tenant: workspace,
      }).toString()}`
    : undefined;
  const requestedProvider = routeProvider ?? firstValue(query.provider);
  // This pre-session value only preserves a safe return target through sign-in.
  // The final default below uses the authenticated deployment profile, which is
  // intentionally unavailable to an unauthenticated browser.
  const resumptionProvider = requestedProvider === "gitlab"
    ? "gitlab"
    : requestedProvider === "github" || githubInstallURL || githubExistingInstallationURL || !gitlabAuthorizeURL
      ? "github"
      : "gitlab";

  // Setup is a resumable workflow, not a generic landing page. Preserve only
  // validated context across an expired login so the user returns to the same
  // workspace and safe step; an absent workspace still resolves to the
  // directory instead of an arbitrary tenant.
  const returnTo = new URLSearchParams();
  if (workspace) {
    returnTo.set("tenant", workspace);
    returnTo.set("next", next);
    // Do not turn the unauthenticated fallback into an explicit selection.
    // A GitLab-token-only deployment can be identified only after sign-in,
    // when the tenant-authorized profile becomes available.
    if (requestedProvider === "github" || requestedProvider === "gitlab") {
      returnTo.set("provider", resumptionProvider);
    }
    if (resumedStep) returnTo.set("step", resumedStep);
  }
  await requireConsoleSession(
    returnTo.size ? `/setup?${returnTo.toString()}` : "/workspaces",
  );
  if (!workspace) redirect("/workspaces");
  const workspaceHome = `/${workspace}/home`;
  const workspaceNext = next.startsWith(`/${workspace}/`) ? next : workspaceHome;
  const [initialization, setup, reviewConfig, providerProfiles, ruleCatalog] =
    await Promise.all([
      getWorkspaceInitialization(workspace),
      getWorkspaceSetupCheckpoint(workspace),
      getReviewConfigData(workspace, "general", "tenant"),
      getProviderProfiles(workspace),
      getRuleCatalog(workspace),
    ]);

  if (initialization.needsSignIn) {
    redirect(sessionRenewalPath(returnTo.size ? `/setup?${returnTo.toString()}` : "/workspaces"));
  }

  if (initialization.status === "access_denied") {
    // Setup is tenant-scoped configuration. A direct URL must obey the same
    // membership boundary as the Console layout and never expose a foreign
    // workspace as an install target.
    redirect("/workspaces?notice=access_denied");
  }
  if (initialization.status === "ready") redirect(workspaceNext);

  const gitlabProfile = providerProfiles.profiles.find(
    (profile) => profile.provider === "gitlab",
  );
  const gitlabDeploymentTokenURL = gitlabProfile?.deployment_token_available
    ? `/api/setup/gitlab/deployment-token?${new URLSearchParams({
        next,
        tenant: workspace,
      }).toString()}`
    : undefined;
  const gitlabAuthorizationAvailable = Boolean(
    gitlabAuthorizeURL || gitlabDeploymentTokenURL,
  );
  // GitHub App is primary when both providers are available. A GitLab-only
  // deployment, including a self-managed deployment token installation,
  // starts directly on GitLab after the authenticated profile is read.
  const provider = requestedProvider === "gitlab"
    ? "gitlab"
    : requestedProvider === "github" || githubInstallURL || githubExistingInstallationURL || !gitlabAuthorizationAvailable
      ? "github"
      : "gitlab";
  const initialProvider = provider;
  const authorization = decodeProviderAuthorization(
    (await cookies()).get(PROVIDER_AUTHORIZATION_COOKIE)?.value,
  );
  const providerAuthorizationReady =
    authorization?.tenant === workspace &&
    authorization.provider === initialProvider;
  const repositoryData = (initialization.status === "connection_failed" ||
    (initialization.status === "needs_setup" && setup.checkpoint?.current_step === "review_scope")) &&
    initialization.connection
    ? await getInstallationRepositories(workspace, initialization.connection.id)
    : undefined;

  return (
    <ConnectSourceControl
      canReachControlPlane={initialization.status !== "unavailable"}
      controlPlaneDetail={initialization.detail}
      githubInstallURL={githubInstallURL}
      githubExistingInstallationURL={githubExistingInstallationURL}
      gitlabAuthorizeURL={gitlabAuthorizeURL}
      gitlabDeploymentTokenURL={gitlabDeploymentTokenURL}
      gitlabProfile={gitlabProfile}
      gitlabProfileDetail={providerProfiles.detail}
      gitlabProfileSource={providerProfiles.source}
      initialProvider={initialProvider}
      next={workspaceNext}
      notice={firstValue(query.notice)}
      initialization={initialization}
      checkpoint={setup.checkpoint}
      generalConfig={reviewConfig.config}
      generalConfigDetail={reviewConfig.detail}
      generalConfigSource={reviewConfig.source}
      ruleCatalog={ruleCatalog}
      repositoryData={repositoryData}
      providerAuthorizationReady={providerAuthorizationReady}
      providerAuthorScopedAvailable={Boolean(providerAuthorizationReady && authorization?.actorExternalID)}
      requestedStep={resumedStep}
      workspace={workspace}
      />
  );
}
