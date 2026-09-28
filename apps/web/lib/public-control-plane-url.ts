// CLI examples are rendered by Server Components. Never derive this address
// from the request Host header or the private CONTROL_API_URL service name.
export function resolvePublicControlPlaneURL({
  publicAPIURL,
  appURL,
}: {
  publicAPIURL?: string;
  appURL?: string;
}): string | undefined {
  const candidate = (publicAPIURL?.trim() || appURL?.trim() || "").replace(/\/$/, "");
  if (!candidate) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    return undefined;
  }
  const host = parsed.hostname.toLowerCase();
  const loopback = host === "localhost" || host === "127.0.0.1" || host === "[::1]";
  const reservedExample = ["example.com", "example.net", "example.org"]
    .some((domain) => host === domain || host.endsWith(`.${domain}`));
  if (
    !host || parsed.username || parsed.password || parsed.search || parsed.hash ||
    parsed.pathname !== "/" ||
    reservedExample || host.endsWith(".example") ||
    (host === "control-api" || host === "console")
  ) return undefined;
  if (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && loopback)) return undefined;
  return parsed.origin;
}

export function configuredPublicControlPlaneURL(): string | undefined {
  return resolvePublicControlPlaneURL({
    publicAPIURL: process.env.OPEN_REVIEW_PUBLIC_API_URL,
    appURL: process.env.OPEN_REVIEW_APP_URL,
  });
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export function cliEnvironmentTemplate(publicAPIURL: string, org: string): string {
  return [
    `export OPEN_REVIEW_API_URL=${shellQuote(publicAPIURL)}`,
    `export OPEN_REVIEW_TENANT=${shellQuote(org)}`,
    "# Load OPEN_REVIEW_API_KEY from your secret manager; do not paste it here.",
    ': "${OPEN_REVIEW_API_KEY:?Set OPEN_REVIEW_API_KEY from your secret manager}"',
  ].join("\n");
}

export function cliReviewTemplate({
  publicAPIURL,
  org,
  installationID,
  repository,
}: {
  publicAPIURL: string;
  org: string;
  installationID: string;
  repository?: string;
}): string {
  const lines = [
    cliEnvironmentTemplate(publicAPIURL, org),
    `export OPEN_REVIEW_INSTALLATION=${shellQuote(installationID)}`,
  ];
  if (repository && !repository.includes("*")) {
    lines.push(`export OPEN_REVIEW_REPOSITORY=${shellQuote(repository)}`);
  }
  lines.push(
    "# Set the remaining values from an existing PR or MR before running.",
    ': "${OPEN_REVIEW_REPOSITORY:?Set OPEN_REVIEW_REPOSITORY to owner/repository}"',
    ': "${OPEN_REVIEW_PR_NUMBER:?Set OPEN_REVIEW_PR_NUMBER}"',
    ': "${OPEN_REVIEW_BASE_REF:?Set OPEN_REVIEW_BASE_REF}"',
    ': "${OPEN_REVIEW_BASE_SHA:?Set OPEN_REVIEW_BASE_SHA to its full SHA}"',
    ': "${OPEN_REVIEW_HEAD_REF:?Set OPEN_REVIEW_HEAD_REF}"',
    ': "${OPEN_REVIEW_HEAD_SHA:?Set OPEN_REVIEW_HEAD_SHA to its full SHA}"',
    "openreview review create \\",
    '  --installation "$OPEN_REVIEW_INSTALLATION" \\',
    '  --repository "$OPEN_REVIEW_REPOSITORY" \\',
    '  --number "$OPEN_REVIEW_PR_NUMBER" \\',
    '  --base-ref "$OPEN_REVIEW_BASE_REF" \\',
    '  --base-sha "$OPEN_REVIEW_BASE_SHA" \\',
    '  --head-ref "$OPEN_REVIEW_HEAD_REF" \\',
    '  --head-sha "$OPEN_REVIEW_HEAD_SHA" \\',
    "  --mode security",
  );
  return lines.join("\n");
}
