import { getOIDCConfiguration } from "@/lib/auth/oidc";

export type DeploymentProfile = {
  identity: string;
  identityState: "configured" | "needs_configuration";
  mode: "cloud" | "self_hosted";
  region: string;
};

function displayValue(value: string | undefined, fallback: string) {
  const normalized = value?.trim();
  // Deployment labels are shown to every workspace owner. Do not surface an
  // arbitrary environment value here: a secret accidentally put in an env var
  // must not become browser-visible configuration.
  return normalized && /^[a-zA-Z0-9][a-zA-Z0-9 ._/-]{0,62}$/.test(normalized)
    ? normalized
    : fallback;
}

// A workspace belongs to one control-plane deployment. These attributes are
// intentionally read-only in the browser; changing a region, hosting mode, or
// IdP at workspace-creation time would create a false security boundary.
export function getDeploymentProfile(): DeploymentProfile {
  const mode =
    process.env.OPEN_REVIEW_DEPLOYMENT_MODE?.trim().toLowerCase() === "cloud"
      ? "cloud"
      : "self_hosted";
  const hasOIDC = Boolean(getOIDCConfiguration());

  return {
    mode,
    region: displayValue(
      process.env.OPEN_REVIEW_DEPLOYMENT_REGION,
      mode === "cloud"
        ? "Operator-managed cloud region"
        : "Operator-managed self-hosted region",
    ),
    identity: hasOIDC ? "Casdoor / OIDC" : "OIDC configuration required",
    identityState: hasOIDC ? "configured" : "needs_configuration",
  };
}
