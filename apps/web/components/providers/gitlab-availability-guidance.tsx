import type { DataSource } from "@/lib/control-api";

export function GitLabAvailabilityGuidance({
  profileSource,
}: {
  profileSource: DataSource;
}) {
  if (profileSource !== "live") {
    return (
      <p className="mt-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3.5 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]" role="status">
        GitLab OAuth is not configured on this Console, and the control plane
        could not confirm whether a deployment-managed token is available.
        Restore the provider-profile service and refresh before choosing a
        connection method.
      </p>
    );
  }

  return (
    <details className="mt-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface)] px-3.5 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
      <summary className="luminous-focus cursor-pointer font-medium text-[var(--ls-text)]">
        How to enable GitLab for this deployment
      </summary>
      <div className="mt-3 space-y-2">
        <p>
          Ask your deployment administrator to configure the GitLab OAuth
          application on the Console server: <code>GITLAB_OAUTH_CLIENT_ID</code>,{" "}
          <code>GITLAB_OAUTH_CLIENT_SECRET</code>, and{" "}
          <code>OPEN_REVIEW_PROVIDER_AUTH_STATE_SECRET</code>. Register the exact
          Console callback <code>/api/setup/gitlab/complete</code> in GitLab.
        </p>
        <p>
          For self-managed GitLab, set its browser-reachable HTTPS base with{" "}
          <code>GITLAB_OAUTH_BASE_URL</code>. If the Console reaches a different
          trusted internal address, set <code>GITLAB_OAUTH_INTERNAL_BASE_URL</code>{" "}
          separately. Provider credentials and URLs stay in deployment custody,
          not in this browser.
        </p>
        <p>
          A deployment-managed token is an alternative only when the operator
          enables that capability on the control plane and mounts the token on
          provider-call workers. Refresh this page after configuration.
        </p>
      </div>
    </details>
  );
}
