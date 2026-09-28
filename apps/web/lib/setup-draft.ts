import type {
  ConnectionSetupForm,
  ConnectionSetupStep,
} from "@/components/onboarding/connection-setup-wizard";

export type SetupProvider = "github" | "gitlab";

// Unsaved choices are scoped to both workspace and provider. In particular,
// switching tabs cannot carry a GitHub repository scope into GitLab setup.
export function setupDraftStorageKey(workspace: string, provider: SetupProvider) {
  return `open-review:setup-draft:${workspace}:${provider}`;
}

export type SetupDraft = {
  version: 5;
  provider: SetupProvider;
  repositoryMode: "all" | "selected";
  form: ConnectionSetupForm;
  step: ConnectionSetupStep;
  // Presentation-only history bound. The signed provider receipt, not this
  // browser value, authorizes installation and persisted policy changes.
  furthestStep?: ConnectionSetupStep;
  // Browser state is only a resume hint. The signed, HttpOnly provider receipt
  // remains the sole authorization to save an installation.
  authorizationReady: boolean;
};

export function isSetupDraft(value: unknown): value is SetupDraft {
  if (!value || typeof value !== "object") return false;
  const draft = value as Partial<SetupDraft>;
  const form = draft.form;
  return Boolean(
    draft.version === 5 &&
      (draft.provider === "github" || draft.provider === "gitlab") &&
      (draft.repositoryMode === "all" || draft.repositoryMode === "selected") &&
      typeof draft.authorizationReady === "boolean" &&
      (draft.step === "provider" ||
        draft.step === "install" ||
        draft.step === "repositories" ||
        draft.step === "scope" ||
        draft.step === "learning" ||
        draft.step === "severity" ||
        draft.step === "sync") &&
      (draft.furthestStep === undefined ||
        draft.furthestStep === "provider" ||
        draft.furthestStep === "install" ||
        draft.furthestStep === "repositories" ||
        draft.furthestStep === "scope" ||
        draft.furthestStep === "learning" ||
        draft.furthestStep === "severity" ||
        draft.furthestStep === "sync") &&
      form &&
      typeof form === "object" &&
      typeof form.repositoryScope === "string" &&
      form.repositoryScope.length <= 2048 &&
      typeof form.automaticReviews === "boolean" &&
      (form.authorScope === "all" || form.authorScope === "mine") &&
      (form.minimumSeverity === "low" ||
        form.minimumSeverity === "medium" ||
        form.minimumSeverity === "high" ||
        form.minimumSeverity === "critical"),
  );
}

export function resumableSetupStep(
  draft: SetupDraft | undefined,
  provider: SetupProvider,
  providerAuthorizationReady: boolean,
): ConnectionSetupStep {
  if (!providerAuthorizationReady) return "provider";
  if (draft?.authorizationReady && draft.provider === provider) return draft.step;
  // A URL step or a pre-authorization browser draft cannot skip the actual
  // connection choices after a provider returns.
  return "install";
}

export function recoverSetupDraft(
  value: unknown,
  provider: SetupProvider,
  providerAuthorizationReady: boolean,
): { draft?: SetupDraft; step: ConnectionSetupStep } {
  if (!isSetupDraft(value)) {
    return { step: providerAuthorizationReady ? "install" : "provider" };
  }
  if (providerAuthorizationReady && value.provider !== provider) {
    return { step: "install" };
  }
  // A pre-authorization draft may restore non-secret choices after the
  // provider returns, but cannot restore its old stage. Only the signed
  // provider receipt lets the flow resume at Install.
  if (providerAuthorizationReady && !value.authorizationReady) {
    return { draft: value, step: "install" };
  }
  return {
    draft: value,
    step: resumableSetupStep(value, provider, providerAuthorizationReady),
  };
}
