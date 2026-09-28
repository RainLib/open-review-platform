import { NextRequest, NextResponse } from "next/server";

import {
  LOCAL_SESSION_COOKIE,
  authCookieOptions,
  localPreviewAvailable,
  safeInternalPath,
} from "@/lib/auth/session";
import { applicationOrigin } from "@/lib/auth/oidc";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export function GET(request: NextRequest) {
  if (!localPreviewAvailable()) {
    return new NextResponse(null, { status: 404 });
  }
  const destination = new URL(
    safeInternalPath(request.nextUrl.searchParams.get("next")),
    applicationOrigin(request),
  );
  const response = NextResponse.redirect(destination);
  response.cookies.set(
    LOCAL_SESSION_COOKIE,
    "active",
    authCookieOptions(8 * 60 * 60),
  );
  return response;
}
