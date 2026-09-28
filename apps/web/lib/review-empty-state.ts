import type { PullRequestData, PullRequestView } from "./control-api";

type ReviewEmptyState = {
  title: string;
  detail: string;
  kind: "first-use-empty" | "filtered-empty" | "unavailable";
  action: { href: string; label: string; primary?: boolean };
};

// An empty current view is not necessarily an empty workspace. Return an
// action that changes the user's state, never a link back to the same URL.
export function reviewEmptyState(
  org: string,
  view: PullRequestView,
  query: { q?: string; cursor?: string },
  data: Pick<PullRequestData, "source" | "counts">,
): ReviewEmptyState {
  const base = `/${encodeURIComponent(org)}/reviews`;
  const viewHref = (target: PullRequestView) => `${base}?view=${target}`;
  if (data.source !== "live" && data.source !== "demo") {
    return {
      title: "Pull request data unavailable",
      detail: "The control plane could not load current review runs. No empty result is inferred.",
      kind: "unavailable",
      action: { href: `/${encodeURIComponent(org)}/connect`, label: "Check connections", primary: true },
    };
  }
  if (query.q?.trim()) {
    return {
      title: "No matching pull requests",
      detail: "No current review matches this search in the selected view.",
      kind: "filtered-empty",
      action: { href: viewHref(view), label: "Clear search" },
    };
  }
  if (query.cursor) {
    return {
      title: "No pull requests on this page",
      detail: "The review index may have changed since this page was opened.",
      kind: "filtered-empty",
      action: { href: viewHref(view), label: "Return to first page" },
    };
  }
  if (view !== "all" && data.counts.all > 0) {
    return {
      title: "No pull requests in this view",
      detail: "Other review states are available in the All view.",
      kind: "filtered-empty",
      action: { href: viewHref("all"), label: "View all reviews" },
    };
  }
  if (data.counts.all === 0) {
    return {
      title: data.source === "demo" ? "No sample reviews yet" : "No review runs yet",
      detail: "Connect a repository, then request a review on an existing GitHub PR or GitLab MR. The guide above gives the exact comment commands.",
      kind: "first-use-empty",
      action: { href: "#manual-review-guide", label: "Start a manual review", primary: true },
    };
  }
  return {
    title: "Review list needs attention",
    detail: "The index reports current reviews but returned no rows. Check platform health before relying on this view.",
    kind: "unavailable",
    action: { href: `/${encodeURIComponent(org)}/settings/health`, label: "Check platform health" },
  };
}
