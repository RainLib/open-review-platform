import { NextRequest, NextResponse } from "next/server";

import { localPreviewAvailable } from "@/lib/auth/session";

const idTokenCookie = "open_review_id_token";
const localSessionCookie = "open_review_local_session";

const publicSegments = new Set([
  "api",
  "favicon.ico",
  "sign-in",
  "setup",
  "workspaces",
  "_next",
]);

function isConsolePath(pathname: string) {
  const segment = pathname.split("/")[1];
  return Boolean(
    segment &&
      !segment.includes(".") &&
      !publicSegments.has(segment),
  );
}

function hasLocalPreviewSession(request: NextRequest) {
  return (
    localPreviewAvailable() &&
    request.cookies.get(localSessionCookie)?.value === "active"
  );
}

// Preserve the full destination before the Console layout evaluates access.
// The layout remains the authority for both session and initialization checks;
// this only keeps login/setup recovery from collapsing a deep link to /home.
export function proxy(request: NextRequest) {
  if (!isConsolePath(request.nextUrl.pathname)) return NextResponse.next();

  const returnTo = `${request.nextUrl.pathname}${request.nextUrl.search}`;
  const hasOIDCSession = Boolean(request.cookies.get(idTokenCookie)?.value);
  if (!hasOIDCSession && !hasLocalPreviewSession(request)) {
    const signIn = new URL("/sign-in", request.url);
    signIn.searchParams.set("next", returnTo);
    return NextResponse.redirect(signIn);
  }

  const headers = new Headers(request.headers);
  headers.set("x-open-review-return-to", returnTo);
  return NextResponse.next({ request: { headers } });
}
