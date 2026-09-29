import { PlatformHealthManager } from "@/components/console/platform-health-manager";
import { getPlatformHealthData } from "@/lib/control-api";

type PlatformHealthTab = "overview" | "queues" | "workers" | "providers" | "incidents" | "runbooks";

export default async function PlatformHealthPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ tab?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const allowed: PlatformHealthTab[] = ["overview", "queues", "workers", "providers", "incidents", "runbooks"];
  const tab = allowed.includes(query.tab as PlatformHealthTab) ? query.tab as PlatformHealthTab : "overview";
  const data = await getPlatformHealthData(org);
  return <PlatformHealthManager data={data} org={org} tab={tab} />;
}
