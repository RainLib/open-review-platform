import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { safeInternalPath } from "./safe-path";

export const ID_TOKEN_COOKIE = "open_review_id_token";
export const LOCAL_SESSION_COOKIE = "open_review_local_session";
export { OIDC_ATTEMPT_COOKIE } from "./oidc-attempt-cookie";
export { safeInternalPath } from "./safe-path";

export type ConsoleSession =
  | { kind: "oidc"; idToken: string }
  | { kind: "local" };

export function localPreviewAvailable() {
  return (
    // The standalone Next runtime intentionally runs with NODE_ENV=production
    // even when an operator is exercising a disposable local development
    // deployment.  Do not infer a login bypass from NODE_ENV: require both an
    // explicit opt-in and the control-plane development boundary instead.
    process.env.ENVIRONMENT === "development" &&
    process.env.OPEN_REVIEW_LOCAL_PREVIEW === "true" &&
    Boolean(
      process.env.CONTROL_API_URL &&
        process.env.CONTROL_API_DEVELOPMENT_SUBJECT,
    )
  );
}

export function authCookieOptions(maxAge?: number) {
  return {
    httpOnly: true,
    maxAge,
    path: "/",
    priority: "high" as const,
    sameSite: "lax" as const,
    // The standalone Console intentionally uses NODE_ENV=production even for
    // the explicit local-preview Compose overlay. That overlay is restricted
    // to a development control plane and 127.0.0.1 HTTP so operators can
    // exercise the local GitLab OAuth callback. Every other deployment keeps
    // secure cookies, including a production build with only ENVIRONMENT set.
    secure: process.env.NODE_ENV === "production" && !localPreviewAvailable(),
  };
}

export async function getConsoleSession(): Promise<ConsoleSession | undefined> {
  const jar = await cookies();
  const idToken = jar.get(ID_TOKEN_COOKIE)?.value;
  if (idToken) return { kind: "oidc", idToken };

  if (
    localPreviewAvailable() &&
    jar.get(LOCAL_SESSION_COOKIE)?.value === "active"
  ) {
    return { kind: "local" };
  }

  return undefined;
}

export async function requireConsoleSession(nextPath: string) {
  const session = await getConsoleSession();
  if (session) return session;
  redirect(`/sign-in?next=${encodeURIComponent(safeInternalPath(nextPath))}`);
}
