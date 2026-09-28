import { getConsoleSession, localPreviewAvailable } from "@/lib/auth/session";

export type ControlPlaneRequestConfiguration = {
  baseURL: string;
  headers: Record<string, string>;
};

export async function getControlPlaneRequestConfiguration(): Promise<
  ControlPlaneRequestConfiguration | undefined
> {
  const baseURL = process.env.CONTROL_API_URL?.replace(/\/$/, "");
  if (!baseURL) return undefined;

  const session = await getConsoleSession();
  if (!session) return undefined;

  if (session.kind === "local") {
    const developmentSubject = process.env.CONTROL_API_DEVELOPMENT_SUBJECT;
    if (!localPreviewAvailable() || !developmentSubject) {
      return undefined;
    }
    return {
      baseURL,
      headers: { "X-Development-Subject": developmentSubject },
    };
  }

  return {
    baseURL,
    headers: { Authorization: `Bearer ${session.idToken}` },
  };
}

export async function forwardControlPlaneRequest(
  path: string,
  init?: RequestInit,
) {
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) return undefined;

  return fetch(`${configuration.baseURL}${path}`, {
    ...init,
    cache: "no-store",
    headers: {
      ...configuration.headers,
      ...init?.headers,
    },
  });
}

export function unavailableControlPlaneResponse() {
  return Response.json(
    {
      error:
        "Sign in with the configured organization identity, then connect this browser to the control plane.",
    },
    { status: 401 },
  );
}
