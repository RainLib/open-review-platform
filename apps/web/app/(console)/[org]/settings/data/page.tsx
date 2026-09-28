import { DataGovernanceManager } from "@/components/console/data-governance-manager";
import { getDataGovernanceData } from "@/lib/control-api";

type DataGovernanceTab = "residency" | "retention" | "jobs";

export default async function DataGovernancePage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const data = await getDataGovernanceData(org);
  const tab: DataGovernanceTab = query.tab === "retention" || query.tab === "jobs" ? query.tab : "residency";
  return <DataGovernanceManager data={data} org={org} tab={tab} />;
}
