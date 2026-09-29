import { APIKeyManager } from "@/components/console/api-key-manager";
import { getAPIKeyData } from "@/lib/control-api";
import { configuredPublicControlPlaneURL } from "@/lib/public-control-plane-url";

export default async function APIKeysPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ tab?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const data = await getAPIKeyData(org);
  const tab = query.tab === "revoked" || query.tab === "guide" ? query.tab : "active";
  return <APIKeyManager apiKeys={data.apiKeys} detail={data.detail} observedAt={new Date().toISOString()} org={org} publicAPIURL={configuredPublicControlPlaneURL()} source={data.source} tab={tab} />;
}
