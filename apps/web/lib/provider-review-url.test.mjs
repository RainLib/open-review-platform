import assert from "node:assert/strict";
import { test } from "node:test";

import { providerCommitTarget, providerFileTarget, providerIssueTarget, providerRepositoryTarget, providerReviewCommentTarget, providerReviewDiffTarget, providerReviewTarget } from "./provider-review-url.ts";

test("Agent Issue links distinguish GitHub.com from GitHub Enterprise", () => {
  assert.deepEqual(
    providerIssueTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform", issue_number: 9 }),
    { label: "GitHub", url: "https://github.com/RainLib/open-review-platform/issues/9" },
  );
  assert.deepEqual(
    providerIssueTarget({ provider: "github", api_base_url: "https://ghe.example.com/api/v3", repository: "team/repo", issue_number: 9 }),
    { label: "GitHub Enterprise", url: "https://ghe.example.com/team/repo/issues/9" },
  );
});

test("Agent feedback links retain a self-managed GitLab relative URL", () => {
  const scope = { provider: "gitlab", api_base_url: "https://git.example.com/gitlab/api/v4", repository: "group/project" };
  assert.deepEqual(
    providerIssueTarget({ ...scope, issue_number: 12 }),
    { label: "GitLab Self-Managed", url: "https://git.example.com/gitlab/group/project/-/issues/12" },
  );
  assert.deepEqual(
    providerReviewTarget({ ...scope, review_number: 31 }),
    { label: "GitLab Self-Managed", url: "https://git.example.com/gitlab/group/project/-/merge_requests/31" },
  );
});

test("review Files links target the provider diff including self-managed GitLab paths", () => {
  assert.deepEqual(
    providerReviewDiffTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform", review_number: 3 }),
    { label: "GitHub", url: "https://github.com/RainLib/open-review-platform/pull/3/files" },
  );
  assert.deepEqual(
    providerReviewDiffTarget({ provider: "gitlab", api_base_url: "https://git.example.com/gitlab/api/v4", repository: "group/project", review_number: 31 }),
    { label: "GitLab Self-Managed", url: "https://git.example.com/gitlab/group/project/-/merge_requests/31/diffs" },
  );
});

test("file evidence links require an exact immutable commit and retain line ranges", () => {
  const github = { provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform", path: "internal/api/server.go", head_sha: "A".repeat(40), start_line: 10, end_line: 12 };
  assert.deepEqual(
    providerFileTarget(github),
    { label: "GitHub", pathLabel: github.path, url: `https://github.com/RainLib/open-review-platform/blob/${"a".repeat(40)}/internal/api/server.go#L10-L12` },
  );
  const gitlab = { provider: "gitlab", api_base_url: "https://git.example.com/gitlab/api/v4", repository: "group/project", path: "src/a.ts", head_sha: "b".repeat(64), start_line: 3, end_line: 4 };
  assert.deepEqual(
    providerFileTarget(gitlab),
    { label: "GitLab Self-Managed", pathLabel: gitlab.path, url: `https://git.example.com/gitlab/group/project/-/blob/${"b".repeat(64)}/src/a.ts#L3-4` },
  );
  for (const head_sha of ["", "main", "abcd1234", "../other", "f".repeat(65)]) {
    assert.equal(providerFileTarget({ ...github, head_sha }), undefined);
  }
  for (const path of ["../other.go", "src/../../other.go", "src/./other.go", "src//other.go", "/src/other.go"]) {
    assert.equal(providerFileTarget({ ...github, path }), undefined);
  }
  assert.equal(providerFileTarget({ ...github, repository: "RainLib/../other" }), undefined);
  assert.equal(providerFileTarget({ ...github, repository: "RainLib//other" }), undefined);
});

test("CLI repository and exact commit links retain the provider web base", () => {
  const github = { provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform", head_sha: "a".repeat(40) };
  assert.deepEqual(providerRepositoryTarget(github), { label: "GitHub", url: "https://github.com/RainLib/open-review-platform" });
  assert.deepEqual(providerCommitTarget(github), { label: "GitHub", url: `https://github.com/RainLib/open-review-platform/commit/${"a".repeat(40)}` });

  const selfManaged = { provider: "gitlab", api_base_url: "https://git.example.com/gitlab/api/v4", repository: "group/sub/project", head_sha: "B".repeat(40) };
  assert.deepEqual(providerRepositoryTarget(selfManaged), { label: "GitLab Self-Managed", url: "https://git.example.com/gitlab/group/sub/project" });
  assert.deepEqual(providerCommitTarget(selfManaged), { label: "GitLab Self-Managed", url: `https://git.example.com/gitlab/group/sub/project/-/commit/${"b".repeat(40)}` });
  assert.equal(providerCommitTarget({ ...selfManaged, head_sha: "partial" }), undefined);
  assert.equal(providerCommitTarget({ ...selfManaged, api_base_url: "javascript:alert(1)" }), undefined);
});

test("Agent feedback links target the exact provider comment", () => {
  assert.deepEqual(
    providerReviewCommentTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "RainLib/open-review-platform", review_number: 6, comment_external_id: "91" }),
    { label: "GitHub", url: "https://github.com/RainLib/open-review-platform/pull/6#issuecomment-91" },
  );
  assert.deepEqual(
    providerReviewCommentTarget({ provider: "gitlab", api_base_url: "https://git.example.com/gitlab/api/v4", repository: "group/project", review_number: 31, comment_external_id: "88" }),
    { label: "GitLab Self-Managed", url: "https://git.example.com/gitlab/group/project/-/merge_requests/31#note_88" },
  );
  assert.equal(providerReviewCommentTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "team/repo", review_number: 6, comment_external_id: "not-a-comment" }), undefined);
  assert.deepEqual(
    providerReviewCommentTarget({ provider: "gitlab", api_base_url: "http://git.internal/gitlab/api/v4", repository: "group/project", review_number: 31, comment_external_id: "88" }),
    { label: "GitLab Self-Managed", url: "http://git.internal/gitlab/group/project/-/merge_requests/31#note_88" },
  );
});

test("provider links fail closed on an untrusted or incomplete target", () => {
  assert.equal(providerIssueTarget({ provider: "gitlab", api_base_url: "javascript:alert(1)", repository: "group/project", issue_number: 1 }), undefined);
  assert.equal(providerIssueTarget({ provider: "gitlab", api_base_url: "https://token@git.example.com/api/v4", repository: "group/project", issue_number: 1 }), undefined);
  assert.equal(providerReviewTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "repo", review_number: 1 }), undefined);
  assert.equal(providerIssueTarget({ provider: "github", api_base_url: "https://api.github.com", repository: "team/repo", issue_number: 0 }), undefined);
});
