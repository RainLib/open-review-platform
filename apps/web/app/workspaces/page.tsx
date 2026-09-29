import { WorkspaceDirectory } from "@/components/onboarding/workspace-directory";
import { LuminousPublicFrame } from "@/components/onboarding/luminous-public-frame";
import { requireConsoleSession } from "@/lib/auth/session";
import { getWorkspaceDirectory } from "@/lib/control-api";

export default async function WorkspacesPage({
  searchParams,
}: {
  searchParams: Promise<{ notice?: string | string[]; requested?: string | string[] }>;
}) {
  await requireConsoleSession("/workspaces");
  const query = await searchParams;
  const directory = await getWorkspaceDirectory();
  return (
    <LuminousPublicFrame>
      <WorkspaceDirectory
        detail={directory.detail}
        needsSignIn={directory.needsSignIn}
        notice={query.notice === "access_denied" ? "access_denied" : undefined}
        requestedSlug={typeof query.requested === "string" ? query.requested : undefined}
        source={directory.source}
        workspaces={directory.workspaces}
      />
    </LuminousPublicFrame>
  );
}
