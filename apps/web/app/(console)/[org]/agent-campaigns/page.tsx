import { AgentCampaignManager } from "@/components/console/agent-campaign-manager";
import { getAgentTaskData, getInitializedWorkspaces } from "@/lib/control-api";

export default async function CampaignPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ campaign?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const [data, workspaces] = await Promise.all([getAgentTaskData(org), getInitializedWorkspaces()]);
  const role = workspaces.find(workspace => workspace.slug === org)?.role;
  return <AgentCampaignManager org={org} installations={data.installations} canManage={role === "owner" || role === "admin"} initialID={query.campaign} />;
}
