import { getUiLanguage } from "@/lib/ui-language-server";
import { workflowText } from "@/lib/workflow-copy";
import { RuleCatalogBrowser } from "@/components/console/rule-catalog-browser";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { getConsoleData, getRuleCatalog } from "@/lib/control-api";

export default async function RuleDiscoveryPage({ params }: { params: Promise<{ org: string }> }) {
  const language = await getUiLanguage();
  const t = (source: string) => workflowText(language, source);
  const { org } = await params;
  const [data, catalog] = await Promise.all([getConsoleData(org), getRuleCatalog(org)]);
  return <div className="space-y-7">
    <PolicyPageHeader active="discovery" description={t("Browse only a tenant-approved policy catalog. Repository content and model suggestions never become a rule template without an explicit governed source.")} eyebrow={t("Policy governance")} org={org} source={data.source} title={t("Rule discovery")} />
    <RuleCatalogBrowser catalog={catalog} org={org} />
  </div>;
}
