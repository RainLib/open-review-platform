const fallbackPath = "/workspaces";
const validationOrigin = "https://open-review.invalid";

// This value is later passed to both Next.js redirect() and new URL(). A
// leading slash alone does not make it local: URL parsers treat /\host as a
// network-path reference, and dot-segment normalization can produce //host.
export function safeInternalPath(value: string | null | undefined) {
  if (
    !value ||
    value.length > 2048 ||
    !value.startsWith("/") ||
    value.includes("\\") ||
    /[\u0000-\u001f\u007f]/.test(value)
  ) {
    return fallbackPath;
  }

  try {
    const target = new URL(value, validationOrigin);
    if (target.origin !== validationOrigin || target.pathname.startsWith("//")) {
      return fallbackPath;
    }
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return fallbackPath;
  }
}

// A rejected token may still be present in the HttpOnly cookie. Keep the
// original local destination, but let sign-in offer a fresh OIDC attempt
// instead of redirecting straight back with the same rejected token.
export function sessionRenewalPath(nextPath: string) {
  const search = new URLSearchParams({
    error: "session",
    next: safeInternalPath(nextPath),
  });
  return `/sign-in?${search.toString()}`;
}

export function shouldRedirectExistingSession(errorCode: string | undefined) {
  return errorCode === undefined;
}
