type OIDCMetadata = {
  issuer?: unknown;
  authorization_endpoint?: unknown;
  token_endpoint?: unknown;
};

function sameIssuerEndpoint(value: unknown, issuer: URL): value is string {
  if (typeof value !== "string") return false;
  try {
    const endpoint = new URL(value);
    return (
      endpoint.origin === issuer.origin &&
      endpoint.protocol === issuer.protocol &&
      !endpoint.username &&
      !endpoint.password &&
      !endpoint.hash
    );
  } catch {
    return false;
  }
}

export function validateOIDCMetadata(metadata: OIDCMetadata, configuredIssuer: string) {
  const issuer = new URL(configuredIssuer);
  if (
    metadata.issuer !== configuredIssuer ||
    !sameIssuerEndpoint(metadata.authorization_endpoint, issuer) ||
    !sameIssuerEndpoint(metadata.token_endpoint, issuer)
  ) {
    throw new Error("OIDC discovery response does not match the configured issuer");
  }
  return {
    authorizationEndpoint: metadata.authorization_endpoint,
    tokenEndpoint: metadata.token_endpoint,
  };
}

export async function discoverOIDC(configuration: { issuer: string }) {
  const response = await fetch(
    `${configuration.issuer}/.well-known/openid-configuration`,
    {
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.timeout(5_000),
    },
  );
  if (!response.ok) throw new Error("OIDC discovery failed");
  return validateOIDCMetadata((await response.json()) as OIDCMetadata, configuration.issuer);
}
