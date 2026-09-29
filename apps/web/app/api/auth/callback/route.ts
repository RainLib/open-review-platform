import { NextRequest, NextResponse } from "next/server";

import {
  applicationOrigin,
  decodeAttempt,
  discoverOIDC,
  getOIDCConfiguration,
  mismatchedLocalOrigin,
  sameValue,
  type OIDCAttempt,
} from "@/lib/auth/oidc";
import { oidcAttemptCookieName, oidcAttemptCookieToExpire } from "@/lib/auth/oidc-attempt-cookie";
import {
  ID_TOKEN_COOKIE,
  OIDC_ATTEMPT_COOKIE,
  authCookieOptions,
  safeInternalPath,
} from "@/lib/auth/session";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

function expireAttemptCookie(response: NextResponse, cookieName?: string) {
  if (cookieName) {
    response.cookies.set(cookieName, "", {
      ...authCookieOptions(),
      expires: new Date(0),
    });
  }
  return response;
}

function callbackError(request: NextRequest, code: string, attempt?: OIDCAttempt, cookieToExpire?: string) {
  const location = new URL(
    "/sign-in",
    mismatchedLocalOrigin(request) ?? applicationOrigin(request),
  );
  location.searchParams.set("error", code);
  location.searchParams.set("next", safeInternalPath(attempt?.next));
  return expireAttemptCookie(NextResponse.redirect(location), cookieToExpire);
}

export async function GET(request: NextRequest) {
  const code = request.nextUrl.searchParams.get("code");
  const state = request.nextUrl.searchParams.get("state");
  const providerError = request.nextUrl.searchParams.get("error");
  const scopedCookie = state ? oidcAttemptCookieName(state) : undefined;
  const scopedValue = scopedCookie ? request.cookies.get(scopedCookie)?.value : undefined;
  const legacyValue = request.cookies.get(OIDC_ATTEMPT_COOKIE)?.value;
  const attempt = decodeAttempt(scopedValue ?? legacyValue);
  const matchingAttempt = state && attempt && sameValue(state, attempt.state) ? attempt : undefined;
  const cookieToExpire = oidcAttemptCookieToExpire(
    state,
    scopedValue,
    legacyValue,
    Boolean(matchingAttempt),
  );
  if (mismatchedLocalOrigin(request)) return callbackError(request, "origin", matchingAttempt, cookieToExpire);
  const configuration = getOIDCConfiguration();
  if (!configuration) return callbackError(request, "configuration", matchingAttempt, cookieToExpire);

  if (!attempt || !state || !sameValue(state, attempt.state)) {
    const reason = !state ? "missing_state" : !scopedValue && !legacyValue ? "missing_attempt_cookie"
      : !attempt ? "invalid_attempt_cookie" : "state_mismatch";
    // Never log the authorization code, PKCE verifier, state, cookie, or token.
    console.warn("OIDC callback state rejected", { reason });
    return callbackError(request, "state", matchingAttempt, cookieToExpire);
  }
  if (providerError) {
    const code = providerError === "access_denied" ? "cancelled" : "provider";
    console.warn("OIDC provider declined authorization", { reason: code });
    return callbackError(request, code, attempt, cookieToExpire);
  }
  if (!code) {
    console.warn("OIDC callback missing authorization code");
    return callbackError(request, "provider", attempt, cookieToExpire);
  }

  try {
    const origin = applicationOrigin(request);
    const metadata = await discoverOIDC(configuration);
    const body = new URLSearchParams({
      client_id: configuration.clientID,
      code,
      code_verifier: attempt.codeVerifier,
      grant_type: "authorization_code",
      redirect_uri: new URL("/api/auth/callback", origin).toString(),
    });
    if (configuration.clientSecret) body.set("client_secret", configuration.clientSecret);

    const tokenResponse = await fetch(metadata.tokenEndpoint, {
      body,
      cache: "no-store",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded",
      },
      method: "POST",
      redirect: "error",
      signal: AbortSignal.timeout(10_000),
    });
    if (!tokenResponse.ok) return callbackError(request, "exchange", attempt, cookieToExpire);

    const payload = (await tokenResponse.json()) as {
      expires_in?: unknown;
      id_token?: unknown;
    };
    const idToken = typeof payload.id_token === "string" ? payload.id_token : "";
    if (idToken.split(".").length !== 3) return callbackError(request, "token", attempt, cookieToExpire);

    const expiresIn =
      typeof payload.expires_in === "number" && payload.expires_in > 0
        ? Math.min(Math.floor(payload.expires_in), 24 * 60 * 60)
        : 60 * 60;
    const response = NextResponse.redirect(
      new URL(safeInternalPath(attempt.next), origin),
    );
    response.cookies.set(ID_TOKEN_COOKIE, idToken, authCookieOptions(expiresIn));
    return expireAttemptCookie(response, cookieToExpire);
  } catch {
    return callbackError(request, "unavailable", attempt, cookieToExpire);
  }
}
