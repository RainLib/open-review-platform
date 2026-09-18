import Link from "next/link";
import { GitPullRequest, ListFilter } from "lucide-react";

import {
  DataSourceNotice,
  EmptyData,
} from "@/components/console/data-source-notice";
import { PageTitle } from "@/components/console/console-shell";
import { StatusBadge } from "@/components/console/status-badge";
import { getConsoleData } from "@/lib/control-api";
import { formatTime, shortSHA } from "@/lib/format";

export default async function ReviewsPage({
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
          eyebrow="Review operations"
          title="Review runs"
          description="A durable record for every review execution. Newer revisions supersede earlier work instead of mutating its evidence."
        />
        <div className="lg:w-[360px]">
          <DataSourceNotice data={data} />
        </div>
      </div>
      {data.runs.length === 0 ? (
        <EmptyData
          title="No review runs found"
          detail="The list is intentionally empty until an authenticated control-plane connection is configured."
        />
      ) : (
        <div className="overflow-hidden rounded-2xl border border-white/[0.075] bg-console-surface">
          <div className="flex items-center justify-between border-b border-white/[0.075] px-5 py-4">
            <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
              <GitPullRequest className="size-4 text-cyan-300" />
              Latest runs
            </div>
            <span className="flex items-center gap-1.5 text-xs text-zinc-500">
              <ListFilter className="size-3.5" />
              Last 25 from control plane
            </span>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-white/[0.06] bg-white/[0.015] text-[11px] uppercase tracking-[0.12em] text-zinc-500">
                <tr>
                  <th className="px-5 py-3 font-medium">Review</th>
                  <th className="px-4 py-3 font-medium">Revision</th>
                  <th className="px-4 py-3 font-medium">Trigger</th>
                  <th className="px-4 py-3 font-medium">Started</th>
                  <th className="px-5 py-3 text-right font-medium">State</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/[0.06]">
                {data.runs.map((run) => (
                  <tr
                    className="transition hover:bg-white/[0.025]"
                    key={run.id}
                  >
                    <td className="px-5 py-4">
                      <Link
                        className="block rounded-md outline-none focus-visible:ring-2 focus-visible:ring-cyan-300/70"
                        href={`/${org}/tasks/${run.id}`}
                      >
                        <p className="font-medium text-zinc-200">
                          {run.repository}
                          <span className="ml-1 text-zinc-500">
                            #{run.review_number}
                          </span>
                        </p>
                        <p className="mt-1 font-mono text-xs text-zinc-500">
                          {shortSHA(run.base_sha)} → {shortSHA(run.head_sha)}
                        </p>
                      </Link>
                    </td>
                    <td className="px-4 py-4 font-mono text-xs text-zinc-400">
                      r{run.revision}
                    </td>
                    <td className="px-4 py-4 capitalize text-zinc-400">
                      {run.trigger_kind.replaceAll("_", " ")}
                    </td>
                    <td className="px-4 py-4 text-zinc-500">
                      {formatTime(run.started_at ?? run.created_at)}
                    </td>
                    <td className="px-5 py-4 text-right">
                      <StatusBadge state={run.state} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
