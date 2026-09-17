import Link from "next/link";
import { ArrowUpRight, CircleDollarSign, Clock3, MoreHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { People } from "@/components/workspace/people";
import { StatusPill } from "@/components/workspace/status-pill";
import type { ReviewTask } from "@/lib/demo-data";
import { cn } from "@/lib/utils";

export function TaskTile({ organization, task }: { organization: string; task: ReviewTask }) {
  return (
    <Card className={cn(
      "surface-shadow relative min-h-[210px] border-white/70 py-0 transition-transform duration-200 hover:-translate-y-0.5",
      task.state === "running" && "bg-[linear-gradient(135deg,oklch(0.975_0.018_278),oklch(1_0_0))]",
      task.state === "completed" && "bg-[linear-gradient(135deg,oklch(0.96_0.035_164),oklch(1_0_0))]",
    )}>
      <CardContent className="flex h-full flex-col p-5">
        <div className="flex items-center justify-between gap-3">
          <StatusPill state={task.state} />
          <Button aria-label={`${task.id} 更多操作`} size="icon-sm" variant="ghost"><MoreHorizontal className="size-4" aria-hidden="true" /></Button>
        </div>
        <Link className="mt-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" href={`/${organization}/tasks/${task.id}`}>
          <p className="text-lg font-semibold tracking-tight">{task.repository} <span className="text-muted-foreground">·</span> {task.pullRequest}</p>
          <p className="mt-1 line-clamp-2 text-sm leading-5 text-muted-foreground">{task.summary}</p>
        </Link>
        {task.state === "running" && task.progress !== undefined ? (
          <div className="mt-4" aria-label={`任务进度 ${task.progress}%`} role="progressbar" aria-valuemax={100} aria-valuemin={0} aria-valuenow={task.progress}>
            <div className="h-2 overflow-hidden rounded-full bg-primary/10"><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${task.progress}%` }} /></div>
            <p className="mt-1.5 text-right text-xs font-medium text-primary">{task.progress}%</p>
          </div>
        ) : <div className="flex-1" />}
        <div className="mt-auto flex items-center justify-between pt-4">
          <People people={task.people} />
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span className="flex items-center gap-1"><Clock3 className="size-3.5" aria-hidden="true" />{task.elapsed}</span>
            <span className="flex items-center gap-1"><CircleDollarSign className="size-3.5" aria-hidden="true" />{task.estimatedCost}</span>
          </div>
        </div>
      </CardContent>
      <Link aria-label={`查看 ${task.id}`} className="absolute right-4 bottom-3 flex size-7 items-center justify-center rounded-full bg-background/70 text-muted-foreground opacity-0 transition-opacity hover:text-primary focus-visible:opacity-100 group-hover/card:opacity-100" href={`/${organization}/tasks/${task.id}`}>
        <ArrowUpRight className="size-4" aria-hidden="true" />
      </Link>
    </Card>
  );
}
