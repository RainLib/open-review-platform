import { RuleCatalogBrowser } from "@/components/console/rule-catalog-browser";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { getConsoleData, getRuleCatalog } from "@/lib/control-api";

export default async function RuleDiscoveryPage({ params }: { params: Promise<{ org: string }> }) {
  const { org } = await params;
  const [data, catalog] = await Promise.all([getConsoleData(org), getRuleCatalog(org)]);
  return <div className="space-y-7">
    <PolicyPageHeader active="discovery" description="Browse only a tenant-approved policy catalog. Repository content and model suggestions never become a rule template without an explicit governed source." eyebrow="Policy governance" org={org} source={data.source} title="Rule discovery" />
    <RuleCatalogBrowser catalog={catalog} org={org} />
  </div>;
}
