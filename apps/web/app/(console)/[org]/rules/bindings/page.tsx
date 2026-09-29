import { Radar } from "lucide-react";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleBindingManager } from "@/components/console/rule-binding-manager";
import { RuleRolloutManager } from "@/components/console/rule-rollout-manager";
import { getRuleBindingData } from "@/lib/control-api";

export default async function RuleBindingsPage({ params }: { params: Promise<{ org: string }> }) {
  const { org } = await params;
  const data = await getRuleBindingData(org);
  return <div className="space-y-7">
    <PolicyPageHeader active="bindings" description="Scope immutable published versions, compare changes in Shadow, then expose a bounded Canary with independent evidence." eyebrow="Controlled rollout" org={org} source={data.source} title="Policy bindings" />
    <div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><Radar className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />State changes affect only future admissions. Existing review runs retain their exact immutable rule snapshot.</div>
    <RuleBindingManager bindings={data.bindings} enabled={data.source === "live"} org={org} ruleSets={data.ruleSets} />
    <RuleRolloutManager bindings={data.bindings} enabled={data.source === "live"} org={org} rollouts={data.rollouts} ruleSets={data.ruleSets} />
  </div>;
}
