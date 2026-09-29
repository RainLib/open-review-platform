import { createHash, randomBytes } from "node:crypto";

import { localPreviewAvailable } from "@/lib/auth/session";
export { discoverOIDC } from "./oidc-discovery";

export type OIDCConfiguration = {
  clientID: string;
  clientSecret?: string;
  issuer: string;
};

export type OIDCAttempt = {
  codeVerifier: string;
  next: string;
  state: string;
};

function validIssuer(value: string) {
  try {
    const url = new URL(value);
    return (
      Boolean(url.host) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      (url.protocol === "https:" ||
        (process.env.NODE_ENV !== "production" && url.protocol === "http:"))
    );
  } catch {
    return false;
  }
}

export function getOIDCConfiguration(): OIDCConfiguration | undefined {
  const issuer = process.env.CASDOOR_ISSUER?.replace(/\/$/, "").trim();
  const clientID = (
    process.env.CASDOOR_CLIENT_ID ?? process.env.CASDOOR_AUDIENCE
  )?.trim();
  if (!issuer || !clientID || !validIssuer(issuer)) return undefined;
  return {
    issuer,
    clientID,
    clientSecret: process.env.CASDOOR_CLIENT_SECRET?.trim() || undefined,
  };
}

export function applicationOrigin(request: Request) {
  const configured = process.env.OPEN_REVIEW_APP_URL?.trim();
  if (configured) {
    const url = new URL(configured);
    if (
      !url.host ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      url.pathname !== "/" ||
      (url.protocol !== "https:" &&
        !(localPreviewAvailable() && url.protocol === "http:"))
    ) {
      throw new Error("OPEN_REVIEW_APP_URL must be a plain HTTP(S) origin");
    }
    return url.origin;
  }

  if (process.env.NODE_ENV === "production") {
    throw new Error("OPEN_REVIEW_APP_URL is required in production");
  }
  const forwardedHost = request.headers
    .get("x-forwarded-host")
    ?.split(",")[0]
    ?.trim();
  const host = forwardedHost || request.headers.get("host");
  const forwardedProtocol = request.headers
    .get("x-forwarded-proto")
    ?.split(",")[0]
    ?.trim();
  const protocol = forwardedProtocol || new URL(request.url).protocol.slice(0, -1);
  if (host && (protocol === "http" || protocol === "https")) {
    return new URL(`${protocol}://${host}`).origin;
  }
  return new URL(request.url).origin;
}

// A local development URL cannot complete an OIDC flow registered for a
// different public origin. Only use the loopback request URL to report that
// mismatch; never use it as a replacement provider redirect_uri.
export function mismatchedLocalOrigin(request: Request) {
  const url = new URL(request.url);
  // Next.js may normalize request.url to localhost in development even when
  // the browser used 127.0.0.1. The Host header retains that exact local
  // callback host; accept it only after a strict loopback-host check.
  const host = request.headers.get("host")?.trim().toLowerCase() ?? url.host;
  if (!/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/.test(host)) {
    return undefined;
  }
  const forwardedProtocol = request.headers.get("x-forwarded-proto")?.split(",")[0]?.trim();
  const protocol = forwardedProtocol === "https" ? "https:" : url.protocol;
  const localOrigin = new URL(`${protocol}//${host}`).origin;
  return localOrigin === applicationOrigin(request) ? undefined : localOrigin;
}

export function newOIDCAttempt(next: string): OIDCAttempt {
  return {
    codeVerifier: randomBytes(48).toString("base64url"),
    next,
    state: randomBytes(32).toString("base64url"),
  };
}

export function pkceChallenge(verifier: string) {
  return createHash("sha256").update(verifier).digest("base64url");
}

export function encodeAttempt(attempt: OIDCAttempt) {
  return Buffer.from(JSON.stringify(attempt)).toString("base64url");
}

export function decodeAttempt(value: string | undefined): OIDCAttempt | undefined {
  if (!value) return undefined;
  try {
    const parsed = JSON.parse(
      Buffer.from(value, "base64url").toString("utf8"),
    ) as Partial<OIDCAttempt>;
    if (
      typeof parsed.codeVerifier !== "string" ||
      typeof parsed.next !== "string" ||
      typeof parsed.state !== "string"
    ) {
      return undefined;
    }
    return {
      codeVerifier: parsed.codeVerifier,
      next: parsed.next,
      state: parsed.state,
    };
  } catch {
    return undefined;
  }
}

export function sameValue(left: string, right: string) {
  if (left.length !== right.length) return false;
  let difference = 0;
  for (let index = 0; index < left.length; index += 1) {
    difference |= left.charCodeAt(index) ^ right.charCodeAt(index);
  }
  return difference === 0;
}
