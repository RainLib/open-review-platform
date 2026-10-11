import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { MemberManager } from "@/components/console/member-manager";
import { DataFreshness } from "@/components/console/page-state";
import { getMemberData } from "@/lib/control-api";
import { HelpHint } from "@/components/console/help-hint";

export default async function MembersPage({ params }: { params: Promise<{ org: string }> }) {
  const { org } = await params;
  const data = await getMemberData(org);
  return (
    <div className="space-y-7">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Enterprise control plane</p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">Members and roles</h1><HelpHint label="Members and roles">Bind stable identity subjects to tenant-scoped roles. Role changes take effect at the control-plane boundary and are written to the audit trail.</HelpHint></div>
        </div>
        <DataFreshness detail={data.detail} state={data.source} />
      </header>
      <EnterpriseSettingsTabs active="members" org={org} />
      <MemberManager accessRequests={data.accessRequests} actorSubject={data.actorSubject} detail={data.detail} enabled={data.source === "live"} invitations={data.invitations} members={data.members} org={org} source={data.source} />
    </div>
  );
}
