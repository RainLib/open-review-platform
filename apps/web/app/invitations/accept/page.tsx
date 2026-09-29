import { redirect } from "next/navigation";

import { InvitationAcceptance } from "@/components/onboarding/invitation-acceptance";
import { LuminousPublicFrame } from "@/components/onboarding/luminous-public-frame";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { sessionRenewalPath } from "@/lib/auth/safe-path";
import { requireConsoleSession } from "@/lib/auth/session";
import { probeConsoleSession } from "@/lib/control-api";

function invitationPath(token: string) {
  return `/invitations/accept?token=${encodeURIComponent(token)}`;
}

export default async function AcceptInvitationPage({
  searchParams,
}: {
  searchParams: Promise<{ token?: string | string[] }>;
}) {
  const query = await searchParams;
  const token = typeof query.token === "string" ? query.token : "";
  if (!/^[a-f0-9]{64}$/i.test(token)) redirect("/sign-in?error=state");
  await requireConsoleSession(invitationPath(token));
  const sessionState = await probeConsoleSession();
  if (sessionState === "rejected") redirect(sessionRenewalPath(invitationPath(token)));
  return (
    <LuminousPublicFrame>
      {sessionState === "unavailable" ? (
        <main className="mx-auto flex min-h-screen max-w-3xl flex-col justify-center p-5">
          <h1 className="mb-5 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)]">Workspace invitation</h1>
          <PageState
            action={<RecoveryAction href={invitationPath(token)}>Retry access check</RecoveryAction>}
            detail="The control plane could not verify your sign-in. The invitation has not been accepted; retry when access is available."
            kind="unavailable"
            title="Access check unavailable"
          />
        </main>
      ) : <InvitationAcceptance token={token} />}
    </LuminousPublicFrame>
  );
}
