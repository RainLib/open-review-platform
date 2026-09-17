"use client";

import Link from "next/link";
import { ArrowRight, Check, ChevronRight, CircleAlert, GitBranch, PlugZap, ShieldAlert, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ReviewPulse } from "@/components/workspace/review-pulse";
import { TaskTile } from "@/components/workspace/task-tile";
import { WorkspaceShell, useWorkspaceActions } from "@/components/workspace/app-shell";
import { reviewTasks } from "@/lib/demo-data";

const attentionItems = [
  {
    icon: ShieldAlert,
    tone: "bg-destructive/10 text-destructive",
    title: "支付重试逻辑存在关键风险",
    detail: "payments-api · 28 分钟前",
    action: "查看审查",
    href: "tasks/ORP-1842",
  },
  {
    icon: CircleAlert,
    tone: "bg-attention/10 text-attention",
    title: "PII 规则集正在等待审批",
    detail: "identity · 2 小时前",
    action: "开始审批",
    href: "rules",
  },
  {
    icon: PlugZap,
    tone: "bg-muted text-muted-foreground",
    title: "GitLab 连接需要续期",
    detail: "platform · 5 小时前",
    action: "查看连接",
    href: "connect",
  },
];

const healthItems = [
  { icon: Check, label: "队列平稳", detail: "平均等待 2 分钟", tone: "text-calm" },
  { icon: GitBranch, label: "GitHub 已连接", detail: "3 分钟前同步", tone: "text-calm" },
  { icon: Check, label: "GitLab 已连接", detail: "12 分钟前同步", tone: "text-calm" },
  { icon: Sparkles, label: "本月用量", detail: "$1,284 / $2,000", tone: "text-primary" },
];

function HomeContent({ organization }: { organization: string }) {
  const { openCommand } = useWorkspaceActions();

  return (
    <div className="space-y-5 pb-8 md:space-y-6">
      <section className="flex flex-col justify-between gap-5 px-1 pt-4 md:flex-row md:items-end md:pt-8">
        <div>
          <p className="mb-2 flex items-center gap-2 text-sm font-medium text-primary"><Sparkles className="size-4" aria-hidden="true" />审查工作台</p>
          <h1 className="font-heading text-4xl font-semibold tracking-[-0.045em] text-foreground md:text-5xl">早上好，Maya。</h1>
          <p className="mt-2 max-w-xl text-base text-muted-foreground md:text-lg">你的审查系统运行平稳，有三件事情值得现在处理。</p>
        </div>
        <Button className="h-11 rounded-2xl px-5 shadow-lg shadow-primary/20" onClick={openCommand} size="lg">
          <Sparkles className="size-4" aria-hidden="true" />开始审查<ArrowRight className="size-4" aria-hidden="true" />
        </Button>
      </section>

      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.42fr)_minmax(360px,0.92fr)]">
        <ReviewPulse />
        <Card className="surface-shadow border-white/70 bg-card/88 py-0">
          <CardHeader className="border-b border-border/70 px-6 py-5">
            <div className="flex items-center justify-between gap-3">
              <CardTitle className="text-xl tracking-tight">需要你处理</CardTitle>
              <Link className="flex items-center gap-1 text-sm font-medium text-muted-foreground transition-colors hover:text-primary" href={`/${organization}/tasks`}>全部<ArrowRight className="size-3.5" aria-hidden="true" /></Link>
            </div>
          </CardHeader>
          <CardContent className="p-1.5">
            {attentionItems.map(({ icon: Icon, tone, title, detail, action, href }) => (
              <Link className="group flex items-center gap-3 rounded-[18px] px-3 py-3.5 transition-colors hover:bg-surface-tint focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" href={`/${organization}/${href}`} key={title}>
                <span className={`flex size-10 shrink-0 items-center justify-center rounded-2xl ${tone}`}><Icon className="size-5" aria-hidden="true" /></span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-semibold">{title}</span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span>
                </span>
                <span className="hidden rounded-full bg-background px-3 py-1.5 text-xs font-medium text-primary shadow-sm transition-transform group-hover:translate-x-0.5 sm:block">{action}</span>
                <ChevronRight className="size-4 text-muted-foreground sm:hidden" aria-hidden="true" />
              </Link>
            ))}
          </CardContent>
        </Card>
      </section>

      <section>
        <div className="mb-4 flex items-end justify-between gap-4 px-1">
          <div><h2 className="text-xl font-semibold tracking-tight">正在推进</h2><p className="mt-1 text-sm text-muted-foreground">团队最新与正在执行的审查。</p></div>
          <Link className="hidden items-center gap-1 text-sm font-medium text-muted-foreground hover:text-primary sm:flex" href={`/${organization}/tasks`}>查看全部<ArrowRight className="size-3.5" aria-hidden="true" /></Link>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          {reviewTasks.map((task) => <TaskTile key={task.id} organization={organization} task={task} />)}
        </div>
      </section>

      <section className="surface-shadow grid overflow-hidden rounded-[22px] border border-white/70 bg-card/88 sm:grid-cols-2 xl:grid-cols-4">
        {healthItems.map(({ icon: Icon, label, detail, tone }) => <div className="flex items-center gap-3 border-border/70 px-5 py-4 [&:not(:last-child)]:border-b sm:[&:not(:last-child)]:border-b-0 sm:[&:not(:last-child)]:border-r" key={label}><Icon className={`size-4 ${tone}`} aria-hidden="true" /><span><span className="block text-sm font-medium">{label}</span><span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span></span></div>)}
      </section>
    </div>
  );
}

export function HomeDashboard({ organization }: { organization: string }) {
  return <WorkspaceShell organization={organization}><HomeContent organization={organization} /></WorkspaceShell>;
}
