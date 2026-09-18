import Link from "next/link";
import { ArrowUpRight, CircleAlert, Clock3, ListTodo } from "lucide-react";

import {
  DataSourceNotice,
  EmptyData,
} from "@/components/console/data-source-notice";
import { PageTitle } from "@/components/console/console-shell";
import { StatusBadge } from "@/components/console/status-badge";
import { getConsoleData } from "@/lib/control-api";
import { formatTime, isActiveRun, isAttentionRun } from "@/lib/format";

export default async function TasksPage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const data = await getConsoleData(org);
  const tasks = data.runs.filter(
    (run) => isActiveRun(run.state) || isAttentionRun(run.state),
  );
  return (
    <div className="space-y-7">
      <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <PageTitle
          eyebrow="Human attention"
          title="Work queue"
          description="Only runs still moving or requiring intervention appear here. Completed history remains in Reviews."
        />
        <div className="lg:w-[360px]">
          <DataSourceNotice data={data} />
        </div>
      </div>
      {tasks.length === 0 ? (
        <EmptyData
          title="Nothing needs intervention"
          detail="Active and attention-required runs will appear here when the control plane emits them."
        />
      ) : (
        <div className="grid gap-3">
          {tasks.map((run) => (
            <Link
              className="group rounded-2xl border border-white/[0.075] bg-console-surface p-5 transition hover:border-cyan-300/20 hover:bg-console-surface-hover"
              href={`/${org}/tasks/${run.id}`}
              key={run.id}
            >
              <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
                <div>
                  <div className="flex items-center gap-2">
                    <ListTodo className="size-4 text-cyan-300" />
                    <p className="text-sm font-medium text-zinc-100">
                      {run.repository}{" "}
                      <span className="text-zinc-500">
                        #{run.review_number}
                      </span>
                    </p>
                  </div>
                  <p className="mt-2 max-w-2xl text-sm leading-6 text-zinc-400">
                    {isAttentionRun(run.state)
                      ? run.failure_message ||
                        "Review reached a state that needs an explicit human decision."
                      : "The control plane is processing this review through its durable pipeline."}
                  </p>
                  <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-zinc-500">
                    <span className="flex items-center gap-1.5">
                      <Clock3 className="size-3.5" />
                      {formatTime(run.started_at ?? run.created_at)}
                    </span>
                    {isAttentionRun(run.state) ? (
                      <span className="flex items-center gap-1.5 text-amber-200/80">
                        <CircleAlert className="size-3.5" />
                        Review evidence before retrying or changing policy
                      </span>
                    ) : null}
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <StatusBadge state={run.state} />
                  <ArrowUpRight className="size-4 text-zinc-600 transition group-hover:text-cyan-300" />
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
