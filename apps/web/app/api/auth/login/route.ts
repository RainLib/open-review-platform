import { NextRequest, NextResponse } from "next/server";

import {
  applicationOrigin,
  discoverOIDC,
  encodeAttempt,
  getOIDCConfiguration,
  mismatchedLocalOrigin,
  newOIDCAttempt,
  pkceChallenge,
} from "@/lib/auth/oidc";
import { oidcAttemptCookieName } from "@/lib/auth/oidc-attempt-cookie";
import { authCookieOptions, safeInternalPath } from "@/lib/auth/session";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function signInError(request: NextRequest, code: string, origin = applicationOrigin(request)) {
  const location = new URL("/sign-in", origin);
  location.searchParams.set("error", code);
  location.searchParams.set(
    "next",
    safeInternalPath(request.nextUrl.searchParams.get("next")),
  );
  return NextResponse.redirect(location);
}

export async function GET(request: NextRequest) {
  const configuration = getOIDCConfiguration();
  if (!configuration) return signInError(request, "configuration");

  try {
    const origin = applicationOrigin(request);
    const localOrigin = mismatchedLocalOrigin(request);
    if (localOrigin && localOrigin !== origin) {
      // A local URL cannot complete an authorization attempt whose fixed,
      // registered callback belongs to another deployment. Never derive a
      // replacement redirect_uri from a request Host header.
      return signInError(request, "origin", localOrigin);
    }
    const next = safeInternalPath(request.nextUrl.searchParams.get("next"));
    const attempt = newOIDCAttempt(next);
    const metadata = await discoverOIDC(configuration);
    const authorizationURL = new URL(metadata.authorizationEndpoint);
    authorizationURL.searchParams.set("client_id", configuration.clientID);
    authorizationURL.searchParams.set(
      "redirect_uri",
      new URL("/api/auth/callback", origin).toString(),
    );
    authorizationURL.searchParams.set("response_type", "code");
    authorizationURL.searchParams.set("scope", "openid profile email");
    authorizationURL.searchParams.set("state", attempt.state);
    authorizationURL.searchParams.set("code_challenge", pkceChallenge(attempt.codeVerifier));
    authorizationURL.searchParams.set("code_challenge_method", "S256");

    const response = NextResponse.redirect(authorizationURL);
    const attemptCookie = oidcAttemptCookieName(attempt.state);
    if (!attemptCookie) throw new Error("new OIDC state has invalid format");
    response.cookies.set(
      attemptCookie,
      encodeAttempt(attempt),
      authCookieOptions(20 * 60),
    );
    return response;
  } catch {
    return signInError(request, "unavailable");
  }
}
