import { createHash } from "node:crypto";

// Retained for callbacks started before per-attempt cookie names were added.
export const OIDC_ATTEMPT_COOKIE = "open_review_oidc_attempt";

export function oidcAttemptCookieName(state: string) {
  // A 32-byte base64url state has exactly 43 characters. Do not let callback
  // input select arbitrary cookie names or expand the request's cookie scope.
  if (!/^[A-Za-z0-9_-]{43}$/.test(state)) return undefined;
  const suffix = createHash("sha256").update(state).digest("hex").slice(0, 20);
  return `${OIDC_ATTEMPT_COOKIE}_${suffix}`;
}

export function oidcAttemptCookieToExpire(
  state: string | null,
  scopedValue: string | undefined,
  legacyValue: string | undefined,
  legacyMatches: boolean,
) {
  const scopedName = state ? oidcAttemptCookieName(state) : undefined;
  if (scopedName && scopedValue) return scopedName;
  // The legacy cookie was shared across tabs. Never delete another tab's
  // in-flight attempt because an unrelated callback supplied a different state.
  return legacyMatches && legacyValue ? OIDC_ATTEMPT_COOKIE : undefined;
}
