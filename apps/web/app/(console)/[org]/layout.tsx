import { headers } from "next/headers";

import { ConsoleShell } from "@/components/console/console-shell";
import { requireConsoleSession, safeInternalPath } from "@/lib/auth/session";
import { getInitializedWorkspaces } from "@/lib/control-api";
import { requireInitializedWorkspace } from "@/lib/workspace-access";

export default async function WorkspaceLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const requestHeaders = await headers();
  const candidate = safeInternalPath(
    requestHeaders.get("x-open-review-return-to"),
  );
  const nextPath =
    candidate === `/${org}` || candidate.startsWith(`/${org}/`)
      ? candidate
      : `/${org}/home`;
  await requireConsoleSession(nextPath);
  await requireInitializedWorkspace(org, nextPath);
  const workspaces = await getInitializedWorkspaces();
  return (
    <ConsoleShell
      org={org}
      preview={process.env.OPEN_REVIEW_CONSOLE_DEMO === "true"}
      workspaces={workspaces}
    >
      {children}
    </ConsoleShell>
  );
}
