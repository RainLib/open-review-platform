"use client";

import { useState } from "react";
import { Bot, Check, CircleDollarSign, Clock3, FileCode2, GitPullRequest, LoaderCircle, RotateCcw, ShieldCheck, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { People } from "@/components/workspace/people";
import { StatusPill } from "@/components/workspace/status-pill";
import { WorkspaceShell } from "@/components/workspace/app-shell";
import { activeTask, reviewTasks } from "@/lib/demo-data";

const stages = [
  { title: "已确认", detail: "状态评论已发布", duration: "3s", state: "done" },
  { title: "已准入", detail: "预算与权限已检查", duration: "6s", state: "done" },
  { title: "工作区就绪", detail: "已固定 base/head SHA", duration: "11s", state: "done" },
  { title: "理解变更", detail: "已提取上下文", duration: "28s", state: "done" },
  { title: "正在审查", detail: "正在追踪支付边界的重试路径", duration: "1m 26s", state: "active" },
  { title: "正在发布", detail: "等待可发布 findings", duration: "待处理", state: "pending" },
];

const signals = [
  [ShieldCheck, "安全基线已解析", "rs_9f31 · 已载入组织安全与依赖策略", "安全", "text-calm bg-calm/10"],
  [CircleDollarSign, "检测到重复扣费路径", "payments/retry.ts · 部分失败后可能重试", "高优先级", "text-destructive bg-destructive/10"],
  [Bot, "可靠性 agent 已完成", "生成 3 条候选发现，等待规范化", "可靠性", "text-primary bg-primary/10"],
  [Clock3, "发布器等待中", "所有 agent 完成后检查当前 head SHA", "等待", "text-muted-foreground bg-muted"],
] as const;

export function TaskCanvas({ organization, taskId }: { organization: string; taskId: string }) {
  const task = reviewTasks.find((item) => item.id === taskId) ?? activeTask;
  const [cancelRequested, setCancelRequested] = useState(false);

  return (
    <WorkspaceShell organization={organization}>
      <div className="space-y-5 pb-8 pt-4 md:space-y-6 md:pt-8">
        <section className="surface-shadow flex flex-col justify-between gap-4 rounded-[24px] border border-white/70 bg-card/90 px-5 py-5 md:flex-row md:items-center md:px-7">
          <div className="min-w-0">
            <div className="mb-2 flex flex-wrap items-center gap-2"><StatusPill state={cancelRequested ? "attention" : "running"} /><span className="font-mono text-xs text-muted-foreground">{task.id}</span></div>
            <h1 className="truncate text-2xl font-semibold tracking-tight md:text-3xl">{task.title}</h1>
            <p className="mt-1.5 text-sm text-muted-foreground">由 @maya 触发 <span className="px-1">·</span> commit a3f9e2b <span className="px-1">·</span> {task.elapsed}</p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button className="rounded-xl" disabled={cancelRequested} onClick={() => setCancelRequested(true)} variant="destructive"><X className="size-4" aria-hidden="true" />{cancelRequested ? "已请求取消" : "取消执行"}</Button>
            <Button asChild className="rounded-xl" variant="outline"><a href="#signals"><GitPullRequest className="size-4" aria-hidden="true" />打开 Pull Request</a></Button>
          </div>
        </section>

        <div className="grid gap-5 2xl:grid-cols-[minmax(0,1fr)_360px]">
          <div className="space-y-5">
            <Card className="surface-shadow overflow-visible border-white/70 bg-card/90 py-0">
              <CardHeader className="px-6 pt-6 md:px-7">
                <div className="flex flex-wrap items-center justify-between gap-3"><div><CardTitle className="text-xl tracking-tight">审查执行画布</CardTitle><p className="mt-1 text-sm text-muted-foreground">展示高层执行流与可验证信号，不展示模型私有推理。</p></div><span className="flex items-center gap-2 rounded-full bg-live/10 px-3 py-1.5 text-xs font-medium text-live"><span className="size-1.5 animate-pulse rounded-full bg-live" />运行中 · 2m 14s</span></div>
              </CardHeader>
              <CardContent className="overflow-x-auto px-6 pb-7 pt-8 md:px-7">
                <div className="relative min-w-[760px] px-3 pb-3 pt-1">
                  <div aria-hidden="true" className="absolute left-12 right-12 top-[39px] h-px bg-border" />
                  <div className="relative grid grid-cols-6 gap-3">
                    {stages.map((stage) => {
                      const active = stage.state === "active";
                      const done = stage.state === "done";
                      return <div className="relative flex flex-col items-center text-center" key={stage.title}>
                        <span className={`relative z-10 flex size-9 items-center justify-center rounded-full border-4 border-card ${done ? "bg-calm text-white" : active ? "bg-primary text-primary-foreground shadow-[0_0_0_9px_oklch(0.54_0.24_278/12%)]" : "bg-muted text-muted-foreground"}`}>
                          {done ? <Check className="size-4" aria-hidden="true" /> : active ? <LoaderCircle className="size-4 animate-spin" aria-hidden="true" /> : <span className="size-2 rounded-full bg-current" />}
                        </span>
                        <p className="mt-3 text-sm font-semibold">{stage.title}</p>
                        <p className="mt-1 max-w-[120px] text-xs leading-4 text-muted-foreground">{stage.detail}</p>
                        {active ? <span className="mt-3 rounded-full bg-primary/10 px-2 py-1 text-xs font-medium text-primary">24 / 37 个文件 · 68%</span> : <span className="mt-3 text-xs text-muted-foreground">{stage.duration}</span>}
                      </div>;
                    })}
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card className="surface-shadow border-white/70 bg-card/90 py-0" id="signals">
              <CardHeader className="flex-row items-center justify-between px-6 py-5 md:px-7"><div><CardTitle className="text-xl tracking-tight">运行信号</CardTitle><p className="mt-1 text-sm text-muted-foreground">系统事件、规则解析和工具结果会在这里留下可审计记录。</p></div><Button className="rounded-xl" size="sm" variant="outline">全部信号</Button></CardHeader>
              <CardContent className="space-y-2 px-3 pb-4 md:px-4">
                {signals.map(([Icon, title, detail, label, className], index) => (
                  <div className="flex items-center gap-3 rounded-[18px] border border-transparent px-3 py-3 transition-colors hover:border-border hover:bg-surface-tint" key={title}>
                    <span className="w-10 text-right font-mono text-xs text-muted-foreground">{index === 0 ? "2m 12s" : index === 1 ? "1m 47s" : index === 2 ? "1m 03s" : "28s"}</span>
                    <span className={`flex size-9 shrink-0 items-center justify-center rounded-xl ${className}`}><Icon className="size-4" aria-hidden="true" /></span>
                    <span className="min-w-0 flex-1"><span className="block text-sm font-semibold">{title}</span><span className="mt-0.5 block truncate text-xs text-muted-foreground">{detail}</span></span>
                    <span className={`hidden rounded-full px-2.5 py-1 text-xs font-medium sm:inline-flex ${className}`}>{label}</span>
                  </div>
                ))}
              </CardContent>
            </Card>
          </div>

          <aside className="space-y-5">
            <Card className="surface-shadow border-white/70 bg-card/90 py-0">
              <CardContent className="p-5">
                <Tabs defaultValue="context">
                  <TabsList className="grid w-full grid-cols-3 rounded-xl"><TabsTrigger value="context">上下文</TabsTrigger><TabsTrigger value="rules">规则</TabsTrigger><TabsTrigger value="usage">用量</TabsTrigger></TabsList>
                  <TabsContent className="mt-5 space-y-5" value="context">
                    <section><h2 className="text-sm font-semibold">运行上下文</h2><dl className="mt-3 space-y-2.5 text-sm"><div className="flex justify-between"><dt className="text-muted-foreground">状态</dt><dd className="flex items-center gap-1.5 font-medium"><span className="size-1.5 rounded-full bg-live" />运行中</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">尝试次数</dt><dd className="font-medium">1 / 5</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">队列</dt><dd className="font-medium">Standard</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">模式</dt><dd className="font-medium">Deep</dd></div></dl></section>
                    <section><h2 className="text-sm font-semibold">触发命令</h2><code className="mt-2 block overflow-x-auto rounded-xl border bg-surface-tint px-3 py-3 font-mono text-xs text-primary">@openreview review --mode deep</code></section>
                    <section><h2 className="text-sm font-semibold">参与者与自动化</h2><div className="mt-3 flex items-center gap-3"><People people={task.people} /><span className="text-xs leading-4 text-muted-foreground">由 @maya 触发<br />Security · Reliability · Publisher</span></div></section>
                    <section className="rounded-xl bg-muted/70 p-3"><div className="flex gap-2"><RotateCcw className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" /><p className="text-xs leading-5 text-muted-foreground">新提交会在发布前替代本次执行，避免陈旧结果写入新的 diff。</p></div></section>
                  </TabsContent>
                  <TabsContent className="mt-5 space-y-3" value="rules"><p className="text-sm text-muted-foreground">本次任务固定使用快照 <code className="font-mono text-primary">rs_9f31</code>。</p>{["Secure API Baseline", "Payments Reliability", "Repository Rules"].map((rule) => <div className="rounded-xl border p-3" key={rule}><p className="text-sm font-medium">{rule}</p><p className="mt-1 text-xs text-muted-foreground">已锁定版本 · 可审计</p></div>)}</TabsContent>
                  <TabsContent className="mt-5" value="usage"><div className="rounded-2xl bg-surface-tint p-4"><p className="text-xs font-medium text-muted-foreground">预计成本</p><p className="mt-1 text-3xl font-semibold tracking-tight">$0.12</p><div className="mt-4 flex justify-between text-xs text-muted-foreground"><span>18.4k input</span><span>2.1k output</span></div><div className="mt-2 h-1.5 overflow-hidden rounded-full bg-primary/10"><div className="h-full w-[62%] rounded-full bg-live" /></div><p className="mt-2 text-xs text-muted-foreground">已使用本次预算的 62%</p></div></TabsContent>
                </Tabs>
              </CardContent>
            </Card>
            <Card className="surface-shadow border-white/70 bg-card/90 py-0"><CardContent className="flex items-center gap-3 p-4"><span className="flex size-9 items-center justify-center rounded-xl bg-primary/10 text-primary"><FileCode2 className="size-4" aria-hidden="true" /></span><div><p className="text-sm font-semibold">可追溯的执行</p><p className="mt-0.5 text-xs text-muted-foreground">规则、引擎和发布结果都绑定到此 run。</p></div></CardContent></Card>
          </aside>
        </div>
      </div>
    </WorkspaceShell>
  );
}
