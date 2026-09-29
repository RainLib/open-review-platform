import { redirect } from "next/navigation";

type PlatformHealthTab = "overview" | "queues" | "workers" | "providers" | "incidents" | "runbooks";

// Kept for the route named in the first console design. Health is now
// canonical under /settings/health, but bookmarked design-review links must
// retain their selected tab rather than ending at a blank or unrelated page.
export default async function LegacyPlatformHealthPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const allowed: PlatformHealthTab[] = ["overview", "queues", "workers", "providers", "incidents", "runbooks"];
  const tab = allowed.includes(query.tab as PlatformHealthTab) ? `?tab=${query.tab}` : "";

  redirect(`/${encodeURIComponent(org)}/settings/health${tab}`);
}
