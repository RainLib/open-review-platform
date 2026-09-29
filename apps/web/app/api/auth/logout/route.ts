import { NextRequest, NextResponse } from "next/server";

import {
  ID_TOKEN_COOKIE,
  LOCAL_SESSION_COOKIE,
  authCookieOptions,
  safeInternalPath,
} from "@/lib/auth/session";
import { applicationOrigin } from "@/lib/auth/oidc";

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(request: NextRequest) {
  const form = await request.formData();
  const next = typeof form.get("next") === "string" ? String(form.get("next")) : "/";
  const response = NextResponse.redirect(
    new URL(safeInternalPath(next), applicationOrigin(request)),
    { status: 303 },
  );
  const expired = { ...authCookieOptions(), expires: new Date(0) };
  response.cookies.set(ID_TOKEN_COOKIE, "", expired);
  response.cookies.set(LOCAL_SESSION_COOKIE, "", expired);
  return response;
}
