import assert from "node:assert/strict";
import { test } from "node:test";

import { discoverOIDC, validateOIDCMetadata } from "./oidc-discovery.ts";

const issuer = "https://casdoor.rainlib.com";
const validMetadata = {
  issuer,
  authorization_endpoint: `${issuer}/login/oauth/authorize`,
  token_endpoint: `${issuer}/api/login/oauth/access_token`,
};

test("OIDC metadata accepts the configured Casdoor issuer and its endpoints", () => {
  assert.deepEqual(validateOIDCMetadata(validMetadata, issuer), {
    authorizationEndpoint: validMetadata.authorization_endpoint,
    tokenEndpoint: validMetadata.token_endpoint,
  });
});

test("OIDC metadata rejects a changed issuer or redirected credential endpoint", () => {
  for (const metadata of [
    { ...validMetadata, issuer: "https://other.example" },
    { ...validMetadata, token_endpoint: "https://other.example/token" },
    { ...validMetadata, token_endpoint: `https://user:secret@casdoor.rainlib.com/token` },
    { ...validMetadata, authorization_endpoint: `${issuer}/authorize#fragment` },
    { ...validMetadata, authorization_endpoint: "/relative/authorize" },
    { ...validMetadata, token_endpoint: undefined },
  ]) {
    assert.throws(() => validateOIDCMetadata(metadata, issuer));
  }
});

test("OIDC discovery refuses HTTP redirects and bounds the fetch", async () => {
  const originalFetch = globalThis.fetch;
  let observedURL;
  let observedOptions;
  globalThis.fetch = async (url, options) => {
    observedURL = url;
    observedOptions = options;
    return Response.json(validMetadata);
  };
  try {
    assert.deepEqual(await discoverOIDC({ issuer }), {
      authorizationEndpoint: validMetadata.authorization_endpoint,
      tokenEndpoint: validMetadata.token_endpoint,
    });
    assert.equal(observedURL, `${issuer}/.well-known/openid-configuration`);
    assert.equal(observedOptions.redirect, "error");
    assert.equal(observedOptions.cache, "no-store");
    assert.ok(observedOptions.signal instanceof AbortSignal);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
