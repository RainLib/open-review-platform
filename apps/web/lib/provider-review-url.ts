import type { ReviewRun } from "@/lib/control-api";

export type ProviderReviewTarget = {
  label: "GitHub" | "GitHub Enterprise" | "GitLab" | "GitLab Self-Managed";
  url: string;
};

export type ProviderFileTarget = ProviderReviewTarget & {
  pathLabel: string;
};

export type ProviderIssueTarget = ProviderReviewTarget;

type ProviderRepositoryInput = Pick<ReviewRun, "provider" | "api_base_url" | "repository">;

function validCommitSHA(value: string) {
  return /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(value);
}

function providerBase(run: Pick<ReviewRun, "provider" | "api_base_url">) {
  const fallback =
    run.provider === "github" ? "https://api.github.com" : "https://gitlab.com/api/v4";
  try {
    const parsed = new URL(run.api_base_url || fallback);
    // The API base is an authorized installation value from the control plane.
    // Some disconnected self-managed providers intentionally use internal HTTP;
    // this builder only opens a browser link and never appends credentials.
    if ((parsed.protocol !== "https:" && parsed.protocol !== "http:") || parsed.username || parsed.password) return undefined;
    return parsed;
  } catch {
    return undefined;
  }
}

function repositoryPath(repository: string) {
  const segments = repository.split("/");
  return segments.length >= 2 && segments.every((segment) => segment !== "" && segment !== "." && segment !== "..")
    ? segments.map((segment) => encodeURIComponent(segment)).join("/")
    : undefined;
}

function filePath(path: string) {
  const segments = path.split("/");
  // URL parsing normalizes literal dot segments even after encodeURIComponent.
  // A model-supplied `../` must never turn a finding into a link to another file.
  return segments.every((segment) => segment !== "" && segment !== "." && segment !== "..")
    ? segments.map((segment) => encodeURIComponent(segment)).join("/")
    : undefined;
}

/**
 * A self-managed GitLab may be installed below a relative URL, for example
 * `https://git.example.com/gitlab`. Its API endpoint then becomes
 * `https://git.example.com/gitlab/api/v4`, while browser links must retain the
 * `/gitlab` prefix. Building links from `URL.origin` would silently drop it.
 */
function gitLabWebBase(apiBase: URL) {
  const apiPath = apiBase.pathname.replace(/\/+$/, "");
  const webPath = apiPath.endsWith("/api/v4")
    ? apiPath.slice(0, -"/api/v4".length)
    : apiPath;
  const base = new URL(apiBase.origin);
  base.pathname = `${webPath || ""}/`.replace(/^\/?/, "/");
  return base;
}

export function providerRepositoryTarget(input: ProviderRepositoryInput): ProviderReviewTarget | undefined {
  const apiBase = providerBase(input);
  const repository = repositoryPath(input.repository);
  if (!apiBase || !repository) return undefined;
  if (input.provider === "github") {
    const publicGitHub = apiBase.hostname === "api.github.com";
    return {
      label: publicGitHub ? "GitHub" : "GitHub Enterprise",
      url: new URL(`/${repository}`, publicGitHub ? "https://github.com" : apiBase.origin).toString(),
    };
  }
  const publicGitLab = apiBase.hostname === "gitlab.com";
  return {
    label: publicGitLab ? "GitLab" : "GitLab Self-Managed",
    url: new URL(repository, gitLabWebBase(apiBase)).toString(),
  };
}

export function providerCommitTarget(input: ProviderRepositoryInput & Pick<ReviewRun, "head_sha">): ProviderReviewTarget | undefined {
  if (!validCommitSHA(input.head_sha)) return undefined;
  const repository = providerRepositoryTarget(input);
  if (!repository) return undefined;
  return {
    ...repository,
    url: `${repository.url.replace(/\/$/, "")}/${input.provider === "github" ? "commit" : "-/commit"}/${input.head_sha.toLowerCase()}`,
  };
}

export function providerReviewTarget(
  run: Pick<ReviewRun, "provider" | "api_base_url" | "repository" | "review_number">,
): ProviderReviewTarget | undefined {
  const apiBase = providerBase(run);
  const repository = repositoryPath(run.repository);
  if (!apiBase || !repository || !Number.isInteger(run.review_number) || run.review_number <= 0) {
    return undefined;
  }

  if (run.provider === "github") {
    const publicGitHub = apiBase.hostname === "api.github.com";
    return {
      label: publicGitHub ? "GitHub" : "GitHub Enterprise",
      url: new URL(`/${repository}/pull/${run.review_number}`, publicGitHub ? "https://github.com" : apiBase.origin).toString(),
    };
  }

  const publicGitLab = apiBase.hostname === "gitlab.com";
  return {
    label: publicGitLab ? "GitLab" : "GitLab Self-Managed",
    url: new URL(`${repository}/-/merge_requests/${run.review_number}`, gitLabWebBase(apiBase)).toString(),
  };
}

export function providerReviewDiffTarget(
  run: Pick<ReviewRun, "provider" | "api_base_url" | "repository" | "review_number">,
): ProviderReviewTarget | undefined {
  const review = providerReviewTarget(run);
  if (!review) return undefined;
  return { ...review, url: `${review.url}/${run.provider === "github" ? "files" : "diffs"}` };
}

export function providerReviewCommentTarget(input: {
  provider: ReviewRun["provider"];
  api_base_url?: string;
  repository: string;
  review_number: number;
  comment_external_id: string;
}): ProviderReviewTarget | undefined {
  if (!/^[1-9]\d{0,19}$/.test(input.comment_external_id)) return undefined;
  const review = providerReviewTarget(input);
  if (!review) return undefined;
  const url = new URL(review.url);
  url.hash = input.provider === "github"
    ? `issuecomment-${input.comment_external_id}`
    : `note_${input.comment_external_id}`;
  return { ...review, url: url.toString() };
}

export function providerIssueTarget(input: {
  provider: ReviewRun["provider"];
  api_base_url?: string;
  repository: string;
  issue_number: number;
}): ProviderIssueTarget | undefined {
  const apiBase = providerBase(input);
  const repository = repositoryPath(input.repository);
  if (!apiBase || !repository || !Number.isInteger(input.issue_number) || input.issue_number <= 0) {
    return undefined;
  }

  if (input.provider === "github") {
    const publicGitHub = apiBase.hostname === "api.github.com";
    return {
      label: publicGitHub ? "GitHub" : "GitHub Enterprise",
      url: new URL(
        `/${repository}/issues/${input.issue_number}`,
        publicGitHub ? "https://github.com" : apiBase.origin,
      ).toString(),
    };
  }

  const publicGitLab = apiBase.hostname === "gitlab.com";
  return {
    label: publicGitLab ? "GitLab" : "GitLab Self-Managed",
    url: new URL(
      `${repository}/-/issues/${input.issue_number}`,
      gitLabWebBase(apiBase),
    ).toString(),
  };
}

export function providerFileTarget(input: {
  provider: ReviewRun["provider"];
  api_base_url?: string;
  repository: string;
  head_sha: string;
  path: string;
  start_line?: number;
  end_line?: number;
}): ProviderFileTarget | undefined {
  const apiBase = providerBase(input);
  const repository = repositoryPath(input.repository);
  const path = filePath(input.path);
  // File evidence must remain bound to the admitted commit. A branch name or
  // partial SHA would silently point at mutable or unrelated provider content.
  if (!apiBase || !repository || !path || !validCommitSHA(input.head_sha)) return undefined;
  const revision = input.head_sha.toLowerCase();

  const line = Number.isFinite(input.start_line)
    ? Math.max(1, Math.trunc(input.start_line as number))
    : undefined;
  const endLine = line && Number.isFinite(input.end_line)
    ? Math.max(line, Math.trunc(input.end_line as number))
    : line;
  const lineFragment = line
    ? input.provider === "github"
      ? `#L${line}${endLine && endLine > line ? `-L${endLine}` : ""}`
      : `#L${line}${endLine && endLine > line ? `-${endLine}` : ""}`
    : "";

  if (input.provider === "github") {
    const publicGitHub = apiBase.hostname === "api.github.com";
    const base = publicGitHub ? "https://github.com" : apiBase.origin;
    return {
      label: publicGitHub ? "GitHub" : "GitHub Enterprise",
      pathLabel: input.path,
      url: `${new URL(`/${repository}/blob/${revision}/${path}`, base).toString()}${lineFragment}`,
    };
  }

  const publicGitLab = apiBase.hostname === "gitlab.com";
  return {
    label: publicGitLab ? "GitLab" : "GitLab Self-Managed",
    pathLabel: input.path,
    url: `${new URL(`${repository}/-/blob/${revision}/${path}`, gitLabWebBase(apiBase)).toString()}${lineFragment}`,
  };
}
