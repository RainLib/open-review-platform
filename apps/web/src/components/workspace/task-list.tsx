import Link from "next/link";
import { ArrowRight, CircleDollarSign, Clock3, Filter, Plus, Search, SlidersHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { People } from "@/components/workspace/people";
import { StatusPill } from "@/components/workspace/status-pill";
import { WorkspaceShell } from "@/components/workspace/app-shell";
import { reviewTasks } from "@/lib/demo-data";

export function TaskList({ organization }: { organization: string }) {
  return (
    <WorkspaceShell organization={organization}>
      <div className="space-y-5 pb-8 pt-4 md:space-y-6 md:pt-8">
        <section className="flex flex-col justify-between gap-5 px-1 md:flex-row md:items-end">
          <div>
            <p className="mb-2 text-sm font-medium text-primary">执行任务</p>
            <h1 className="text-3xl font-semibold tracking-[-0.04em] md:text-4xl">把每一次审查都看得清楚。</h1>
            <p className="mt-2 max-w-2xl text-base text-muted-foreground">从受理、队列到发布，所有运行都保留可追溯状态与资源边界。</p>
          </div>
          <Button className="rounded-xl"><Plus className="size-4" aria-hidden="true" />开始审查</Button>
        </section>

        <Card className="surface-shadow border-white/70 bg-card/90 py-0">
          <CardHeader className="flex flex-col gap-4 border-b border-border/70 px-5 py-5 md:flex-row md:items-center md:justify-between md:px-6">
            <div className="flex items-center gap-3"><CardTitle className="text-xl tracking-tight">最近任务</CardTitle><span className="rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">3 运行中</span></div>
            <div className="flex flex-1 items-center gap-2 md:max-w-md">
              <div className="relative flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" /><Input aria-label="搜索任务" className="h-9 rounded-xl border-transparent bg-muted/70 pl-9 shadow-none focus-visible:bg-background" placeholder="搜索任务、仓库或 PR" /></div>
              <Button aria-label="筛选任务" size="icon" variant="outline"><Filter className="size-4" aria-hidden="true" /></Button>
            </div>
          </CardHeader>
          <CardContent className="p-0">
            <div className="hidden grid-cols-[minmax(220px,1.35fr)_110px_145px_150px_120px_44px] items-center gap-4 border-b border-border/70 px-6 py-3 text-xs font-medium text-muted-foreground lg:grid">
              <span>任务</span><span>状态</span><span>执行模式</span><span>参与者</span><span>资源</span><span aria-label="操作" />
            </div>
            <div className="divide-y divide-border/70">
              {reviewTasks.map((task) => (
                <Link className="group grid gap-3 px-5 py-5 transition-colors hover:bg-surface-tint focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring lg:grid-cols-[minmax(220px,1.35fr)_110px_145px_150px_120px_44px] lg:items-center lg:gap-4 lg:px-6" href={`/${organization}/tasks/${task.id}`} key={task.id}>
                  <span className="min-w-0"><span className="flex items-center gap-2"><span className="font-mono text-xs text-primary">{task.id}</span><span className="hidden text-xs text-muted-foreground sm:inline">{task.pullRequest}</span></span><span className="mt-1 block truncate text-sm font-semibold">{task.title}</span><span className="mt-1 block text-xs text-muted-foreground">{task.repository}</span></span>
                  <span><StatusPill state={task.state} /></span>
                  <span className="flex items-center gap-2 text-sm"><span className="size-2 rounded-full bg-primary" /><span>{task.mode}</span></span>
                  <People people={task.people} />
                  <span className="flex items-center gap-3 text-xs text-muted-foreground"><span className="flex items-center gap-1"><Clock3 className="size-3.5" aria-hidden="true" />{task.elapsed}</span><span className="flex items-center gap-1"><CircleDollarSign className="size-3.5" aria-hidden="true" />{task.estimatedCost}</span></span>
                  <ArrowRight className="hidden size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-primary lg:block" aria-hidden="true" />
                </Link>
              ))}
            </div>
          </CardContent>
        </Card>

        <section className="surface-shadow flex flex-col gap-4 rounded-[22px] border border-white/70 bg-card/90 p-5 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3"><span className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary"><SlidersHorizontal className="size-4" aria-hidden="true" /></span><span><span className="block text-sm font-semibold">任务策略正在保护队列</span><span className="mt-1 block text-xs text-muted-foreground">每个任务均在受理前固定规则快照、预算和发布条件。</span></span></div>
          <Link className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline" href={`/${organization}/rules`}>查看策略<ArrowRight className="size-3.5" aria-hidden="true" /></Link>
        </section>
      </div>
    </WorkspaceShell>
  );
}
