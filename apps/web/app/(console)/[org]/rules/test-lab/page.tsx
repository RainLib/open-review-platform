import { FlaskConical } from "lucide-react";
import { PolicyPageHeader } from "@/components/console/policy-page-header";
import { RuleTestLab } from "@/components/console/rule-test-lab";
import { getConsoleData } from "@/lib/control-api";

export default async function RuleTestLabPage({ params }: { params: Promise<{ org: string }> }) {
  const { org } = await params;
  const data = await getConsoleData(org);
  return <div className="space-y-7"><PolicyPageHeader active="test-lab" description="Compile one exact rule version against a repository baseline, inspect deterministic deltas, and measure the available historical sample before approval." eyebrow="Policy evidence" org={org} source={data.source} title="Test Lab" /><div className="flex items-start gap-3 rounded-[14px] border border-violet-500/20 bg-violet-500/[0.07] px-4 py-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><FlaskConical className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />Preview and replay are read-only. They never publish provider comments, checks, or merge-gate status.</div><RuleTestLab enabled={data.source === "live"} org={org} ruleSets={data.ruleSets} runs={data.runs} /></div>;
}
