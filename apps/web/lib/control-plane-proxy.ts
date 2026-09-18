type ProxyConfiguration = {
  baseURL: string;
  developmentSubject: string;
};

export function getDevelopmentControlPlaneConfiguration():
  | ProxyConfiguration
  | undefined {
  const baseURL = process.env.CONTROL_API_URL?.replace(/\/$/, "");
  const developmentSubject = process.env.CONTROL_API_DEVELOPMENT_SUBJECT;

  if (
    !baseURL ||
    !developmentSubject ||
    process.env.NODE_ENV === "production"
  ) {
    return undefined;
  }

  return { baseURL, developmentSubject };
}

export async function forwardDevelopmentRequest(
  path: string,
  init?: RequestInit,
) {
  const configuration = getDevelopmentControlPlaneConfiguration();
  if (!configuration) return undefined;

  return fetch(`${configuration.baseURL}${path}`, {
    ...init,
    cache: "no-store",
    headers: {
      ...init?.headers,
      "X-Development-Subject": configuration.developmentSubject,
    },
  });
}

export function unavailableDevelopmentResponse() {
  return Response.json(
    {
      error:
        "The interactive control-plane bridge is available only with a local development subject.",
    },
    { status: 503 },
  );
}
