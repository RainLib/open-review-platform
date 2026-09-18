import { FileCheck2, ShieldCheck } from "lucide-react";

import {
  DataSourceNotice,
  EmptyData,
} from "@/components/console/data-source-notice";
import {
  ImplementationNotice,
  PageTitle,
} from "@/components/console/console-shell";
import { getConsoleData } from "@/lib/control-api";
import { formatTime } from "@/lib/format";

export default async function RulesPage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const data = await getConsoleData(org);
  return (
    <div className="space-y-7">
      <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <PageTitle
          eyebrow="Governance"
          title="Policy library"
          description="Rule sets are reviewed and published as immutable versions; scope bindings are separately audited."
        />
        <div className="lg:w-[360px]">
          <DataSourceNotice data={data} />
        </div>
      </div>
      <ImplementationNotice>
        Read access is available now. Rule creation, approval and publication
        endpoints exist in the control plane; their mutation UI will be added
        only with authenticated Casdoor session handling and permission-aware
        flows.
      </ImplementationNotice>
      {data.ruleSets.length === 0 ? (
        <EmptyData
          title="No policy sets found"
          detail="Create a rule set through the existing control-plane API, or enable demo mode to inspect the console layout."
        />
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {data.ruleSets.map((set) => (
            <article
              className="rounded-2xl border border-white/[0.075] bg-console-surface p-5"
              key={set.id}
            >
              <div className="flex items-start justify-between gap-3">
                <span className="grid size-9 place-items-center rounded-xl bg-cyan-300/[0.08] text-cyan-200">
                  <ShieldCheck className="size-4" />
                </span>
                <span className="rounded-full border border-emerald-300/20 bg-emerald-300/[0.06] px-2 py-0.5 text-xs font-medium text-emerald-200">
                  Versioned
                </span>
              </div>
              <h2 className="mt-5 text-base font-medium text-zinc-100">
                {set.name}
              </h2>
              <p className="mt-2 min-h-12 text-sm leading-6 text-zinc-400">
                {set.description ||
                  "No description was provided for this rule set."}
              </p>
              <div className="mt-5 flex items-center justify-between border-t border-white/[0.06] pt-3 text-xs text-zinc-500">
                <span className="flex items-center gap-1.5">
                  <FileCheck2 className="size-3.5" />
                  Updated {formatTime(set.updated_at)}
                </span>
                <span className="font-mono">{set.id.slice(0, 8)}</span>
              </div>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}
