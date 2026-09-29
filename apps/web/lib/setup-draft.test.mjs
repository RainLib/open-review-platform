import assert from "node:assert/strict";
import { test } from "node:test";

import { isSetupDraft, recoverSetupDraft, resumableSetupStep, setupDraftStorageKey } from "./setup-draft.ts";

const draft = {
  version: 5,
  provider: "github",
  repositoryMode: "selected",
  form: {
    repositoryScope: "RainLib/open-review-platform",
    automaticReviews: false,
    authorScope: "mine",
    minimumSeverity: "high",
  },
  step: "sync",
  authorizationReady: true,
};

test("only current, bounded setup drafts can be restored", () => {
  assert.equal(isSetupDraft(draft), true);
  assert.equal(isSetupDraft({ ...draft, version: 4 }), false);
  assert.equal(isSetupDraft({ ...draft, authorizationReady: undefined }), false);
  assert.equal(isSetupDraft({ ...draft, furthestStep: "unknown" }), false);
  assert.equal(isSetupDraft({ ...draft, furthestStep: "sync" }), true);
  assert.equal(isSetupDraft({ ...draft, form: { ...draft.form, repositoryScope: "x".repeat(2049) } }), false);
});

test("unsaved setup choices use separate keys for each provider and workspace", () => {
  assert.notEqual(setupDraftStorageKey("acme", "github"), setupDraftStorageKey("acme", "gitlab"));
  assert.notEqual(setupDraftStorageKey("acme", "gitlab"), setupDraftStorageKey("other", "gitlab"));
});

test("switching providers retains only each provider's own unsaved choices", () => {
  const drafts = new Map([
    [setupDraftStorageKey("acme", "github"), draft],
    [setupDraftStorageKey("acme", "gitlab"), {
      ...draft,
      provider: "gitlab",
      repositoryMode: "selected",
      form: { ...draft.form, repositoryScope: "team/backend", minimumSeverity: "critical" },
      step: "repositories",
      authorizationReady: false,
    }],
  ]);
  const gitlab = recoverSetupDraft(drafts.get(setupDraftStorageKey("acme", "gitlab")), "gitlab", false);
  assert.equal(gitlab.step, "provider");
  assert.equal(gitlab.draft?.form.repositoryScope, "team/backend");
  const github = recoverSetupDraft(drafts.get(setupDraftStorageKey("acme", "github")), "github", true);
  assert.equal(github.step, "sync");
  assert.equal(github.draft?.form.repositoryScope, "RainLib/open-review-platform");
  const gitlabAuthorized = recoverSetupDraft(drafts.get(setupDraftStorageKey("acme", "gitlab")), "gitlab", true);
  assert.equal(gitlabAuthorized.step, "install");
  assert.equal(gitlabAuthorized.draft?.form.minimumSeverity, "critical");
  assert.equal(recoverSetupDraft(drafts.get(setupDraftStorageKey("acme", "github")), "gitlab", true).draft, undefined);
});

test("a signed provider return cannot use a URL or pre-authorization draft to skip setup", () => {
  assert.equal(resumableSetupStep(undefined, "github", true), "install");
  assert.equal(resumableSetupStep({ ...draft, authorizationReady: false }, "github", true), "install");
  assert.equal(resumableSetupStep({ ...draft, provider: "gitlab" }, "github", true), "install");
  assert.equal(resumableSetupStep(draft, "github", false), "provider");
});

test("a same-provider post-authorization draft resumes choices after login expiry", () => {
  assert.equal(resumableSetupStep(draft, "github", true), "sync");
  assert.equal(resumableSetupStep({ ...draft, step: "severity" }, "github", true), "severity");
  const recovery = recoverSetupDraft(draft, "github", true);
  assert.equal(recovery.step, "sync");
  assert.deepEqual(recovery.draft?.form, draft.form);
  const preAuthorization = recoverSetupDraft({ ...draft, authorizationReady: false }, "github", true);
  assert.equal(preAuthorization.step, "install");
  assert.deepEqual(preAuthorization.draft?.form, draft.form);
  assert.equal(recoverSetupDraft({ ...draft, provider: "gitlab" }, "github", true).draft, undefined);
  assert.equal(recoverSetupDraft(draft, "github", false).step, "provider");
});
