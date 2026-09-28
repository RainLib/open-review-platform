import { redirect } from "next/navigation";

import { getWorkspaceInitialization } from "@/lib/control-api";
import { sessionRenewalPath } from "@/lib/auth/safe-path";

export async function requireInitializedWorkspace(
  org: string,
  nextPath: string,
) {
  const initialization = await getWorkspaceInitialization(org);
  if (initialization.needsSignIn) {
    redirect(sessionRenewalPath(nextPath));
  }
  if (initialization.status === "ready") return;

  if (initialization.status === "access_denied") {
    // A membership failure must not become a setup invitation or reveal
    // whether the requested workspace exists.
    const denied = new URLSearchParams({ notice: "access_denied", requested: org });
    redirect(`/workspaces?${denied.toString()}`);
  }

  const search = new URLSearchParams({
    next: nextPath,
    tenant: org,
  });
  if (initialization.status === "unavailable") {
    search.set("notice", "verification");
  }
  redirect(`/setup?${search.toString()}`);
}
