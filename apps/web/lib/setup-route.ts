import type { ConnectionSetupStep } from "../components/onboarding/connection-setup-wizard";
import type { SetupProvider } from "./setup-draft";

const wizardOrder: ConnectionSetupStep[] = [
  "provider", "install", "repositories", "scope", "learning", "severity", "sync",
];

export function laterSetupStep(a: ConnectionSetupStep, b: ConnectionSetupStep): ConnectionSetupStep {
  return wizardOrder.indexOf(a) >= wizardOrder.indexOf(b) ? a : b;
}

export function setupRouteForStep(step: ConnectionSetupStep, provider: SetupProvider) {
  switch (step) {
    case "provider": return "/setup/provider";
    case "install": return `/setup/${provider}`;
    case "repositories": return "/setup/repositories";
    case "scope": return "/setup/review-scope";
    case "learning": return "/setup/learning";
    case "severity": return "/setup/severity";
    case "sync": return "/setup/verify";
  }
}

export function setupStepFromSegment(segment: string): ConnectionSetupStep | undefined {
  switch (segment) {
    case "provider": return "provider";
    case "github":
    case "gitlab": return "install";
    case "repositories": return "repositories";
    case "review-scope": return "scope";
    case "learning": return "learning";
    case "severity": return "severity";
    case "verify": return "sync";
    default: return undefined;
  }
}

// A route is a presentation hint, not an authorization or a completed step.
// The signed provider receipt and the provider-scoped browser draft determine
// how far this tab may resume without replaying its setup choices.
export function restorableRequestedStep(
  recovered: ConnectionSetupStep,
  requested: ConnectionSetupStep | undefined,
  authorizationReady: boolean,
  furthestReached: ConnectionSetupStep = recovered,
): ConnectionSetupStep {
  if (!authorizationReady) return "provider";
  if (!requested) return recovered;
  return wizardOrder.indexOf(requested) <= wizardOrder.indexOf(furthestReached)
    ? requested
    : recovered;
}

export function setupRouteForCheckpoint(
  step: "review_scope" | "learning" | "severity" | "rules",
  requestedStep?: ConnectionSetupStep,
) {
  switch (step) {
    case "review_scope": return requestedStep === "scope" ? "/setup/review-scope" : "/setup/repositories";
    case "learning": return "/setup/learning";
    case "severity": return "/setup/severity";
    case "rules": return "/setup/rules";
  }
}
