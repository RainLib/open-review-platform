import { SSOManager } from "@/components/console/sso-manager";
import { getSSOData } from "@/lib/control-api";

type SSOTab = "overview" | "identity-provider" | "domains-mapping";

export default async function SSOPage({
  params,
  searchParams,
}: {
  params: Promise<{ org: string }>;
  searchParams: Promise<{ tab?: string }>;
}) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const data = await getSSOData(org);
  const tab: SSOTab =
    query.tab === "identity-provider" || query.tab === "domains-mapping"
      ? query.tab
      : "overview";

  return <SSOManager data={data} org={org} tab={tab} />;
}
