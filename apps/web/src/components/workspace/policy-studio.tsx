"use client";

import { useState } from "react";
import { ArrowRight, BarChart3, Check, CircleAlert, Code2, Eye, FileLock2, Layers3, Plus, UsersRound } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { WorkspaceShell } from "@/components/workspace/app-shell";
import { cn } from "@/lib/utils";

const ruleSets = [
  { name: "Secure API Baseline", version: "v2.3", description: "守住认证、输入处理、密钥与 API 边界。", findings: "184 findings · 71% resolved", state: "已发布", tone: "text-calm bg-calm/10", selected: true },
  { name: "Payments Reliability", version: "v1.6", description: "预防支付服务中的重试与一致性回归。", findings: "67 findings · 62% resolved", state: "已发布", tone: "text-calm bg-calm/10" },
  { name: "PII Protection", version: "v3.1", description: "检测与防止敏感数据的不安全暴露。", findings: "28 findings · 50% resolved", state: "待审批", tone: "text-attention bg-attention/10" },
  { name: "Repository Craft", version: "v0.9", description: "推动可维护、一致、易审查的代码。", findings: "12 findings · 83% resolved", state: "草稿", tone: "text-muted-foreground bg-muted" },
];

const layers = [
  { icon: FileLock2, title: "组织基线", description: "适用于全部团队的核心安全与合规规则。", count: "12 条规则", status: "已锁定", tone: "bg-primary/10 text-primary" },
  { icon: UsersRound, title: "团队覆盖层", description: "继承组织策略，并附加工程团队约束。", count: "6 条规则", status: "已继承", tone: "bg-sky-100 text-sky-700" },
  { icon: Code2, title: "仓库附加规则", description: "按仓库与路径增加的可选规则。", count: "4 条规则", status: "允许", tone: "bg-calm/10 text-calm" },
];

const policyFeatures = [
  { icon: Check, title: "阻断关键发现", detail: "已启用" },
  { icon: Layers3, title: "合并系统规则", detail: "已启用" },
  { icon: UsersRound, title: "需要安全审批", detail: "2 位批准人" },
];

export function PolicyStudio({ organization }: { organization: string }) {
  const [selected, setSelected] = useState(ruleSets[0].name);
  const selectedRule = ruleSets.find((rule) => rule.name === selected) ?? ruleSets[0];

  return (
    <WorkspaceShell organization={organization}>
      <div className="space-y-5 pb-8 pt-4 md:space-y-6 md:pt-8">
        <section className="flex flex-col justify-between gap-5 px-1 md:flex-row md:items-end">
          <div><p className="mb-2 text-sm font-medium text-primary">策略工作室</p><h1 className="max-w-3xl text-3xl font-semibold tracking-[-0.04em] md:text-4xl">塑造组织审查代码的方式。</h1><p className="mt-2 text-base text-muted-foreground">版本化策略、人工审批与可衡量的效果，汇集在同一个工作区。</p></div>
          <div className="flex gap-2"><Button className="rounded-xl" variant="outline">导入</Button><Button className="rounded-xl"><Plus className="size-4" aria-hidden="true" />新建规则集</Button></div>
        </section>

        <Tabs defaultValue="library"><TabsList className="rounded-xl"><TabsTrigger value="library">规则库</TabsTrigger><TabsTrigger value="assignments">绑定</TabsTrigger><TabsTrigger value="approvals">审批 <Badge className="ml-1 h-4 min-w-4 px-1 text-[10px]" variant="secondary">2</Badge></TabsTrigger><TabsTrigger value="test">测试实验室</TabsTrigger></TabsList></Tabs>

        <section className="grid gap-5 2xl:grid-cols-[minmax(300px,0.78fr)_minmax(520px,1.45fr)_240px]">
          <Card className="surface-shadow border-white/70 bg-card/90 py-0"><CardContent className="p-4"><div className="mb-4 flex items-center justify-between"><h2 className="text-lg font-semibold tracking-tight">规则集</h2><Button size="sm" variant="ghost">筛选</Button></div><div className="space-y-2">{ruleSets.map((rule) => <button className={cn("w-full rounded-[18px] border p-4 text-left transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring", selected === rule.name ? "border-primary bg-surface-tint shadow-sm" : "border-transparent hover:border-border hover:bg-surface-tint/60")} key={rule.name} onClick={() => setSelected(rule.name)} type="button"><div className="flex items-start justify-between gap-2"><p className="text-sm font-semibold">{rule.name}</p><span className="font-mono text-xs text-muted-foreground">{rule.version}</span></div><p className="mt-2 text-xs leading-5 text-muted-foreground">{rule.description}</p><div className="mt-4 flex items-center justify-between"><span className={`rounded-full px-2 py-1 text-[11px] font-medium ${rule.tone}`}>{rule.state}</span><span className="text-[11px] text-muted-foreground">{rule.findings}</span></div></button>)}</div></CardContent></Card>

          <Card className="surface-shadow border-white/70 bg-card/90 py-0"><CardContent className="p-6 md:p-7"><div className="flex flex-wrap items-start justify-between gap-4"><div><div className="flex flex-wrap items-center gap-2"><h2 className="text-2xl font-semibold tracking-tight">{selectedRule.name}</h2><span className="rounded-full bg-calm/10 px-2.5 py-1 text-xs font-medium text-calm">已发布 · {selectedRule.version}</span></div><p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">{selectedRule.description}</p></div><Button size="icon" variant="ghost"><Eye className="size-4" aria-hidden="true" /></Button></div>
            <div className="mt-7 flex gap-2 overflow-x-auto border-b border-border pb-3 text-sm font-medium"><button className="border-b-2 border-primary px-2 pb-2 text-primary" type="button">策略组合</button><button className="px-2 pb-2 text-muted-foreground" type="button">规则 18</button><button className="px-2 pb-2 text-muted-foreground" type="button">测试结果</button><button className="px-2 pb-2 text-muted-foreground" type="button">绑定</button></div>
            <div className="relative mt-6 space-y-3 before:absolute before:bottom-6 before:left-5 before:top-6 before:w-px before:bg-border">{layers.map((layer, index) => { const Icon = layer.icon; return <div className="relative z-10 flex gap-3" key={layer.title}><span className={`mt-5 flex size-10 shrink-0 items-center justify-center rounded-xl ${layer.tone}`}><Icon className="size-5" aria-hidden="true" /></span><div className="flex-1 rounded-[18px] border border-border/70 bg-surface-tint/50 p-4"><div className="flex flex-wrap items-center justify-between gap-2"><p className="text-sm font-semibold">{layer.title}</p><span className="rounded-full bg-background px-2 py-1 text-[11px] font-medium text-muted-foreground">{layer.status}</span></div><p className="mt-1 text-xs leading-5 text-muted-foreground">{layer.description}</p><p className="mt-3 text-xs font-medium text-foreground">{layer.count} <span className="mx-1 text-border">·</span>{index === 0 ? "全部团队" : index === 1 ? "继承组织规则" : "按仓库可选"}</p></div></div>; })}</div>
            <div className="mt-6 grid gap-3 border-t border-border pt-5 sm:grid-cols-3">{policyFeatures.map(({ icon: Icon, title, detail }) => <div className="flex gap-2" key={title}><Icon className="mt-0.5 size-4 text-calm" aria-hidden="true" /><span><span className="block text-xs font-medium">{title}</span><span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span></span></div>)}</div>
            <div className="mt-6 flex flex-col gap-3 rounded-2xl border border-attention/30 bg-attention/10 p-4 sm:flex-row sm:items-center sm:justify-between"><span className="flex gap-2 text-sm text-attention"><CircleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />已发布版本不可变，编辑将创建 v2.4 草稿。</span><Button className="rounded-xl bg-background text-foreground hover:bg-background/80" size="sm">创建草稿</Button></div>
          </CardContent></Card>

          <div className="space-y-5"><Card className="surface-shadow border-white/70 bg-card/90 py-0"><CardContent className="p-5"><h2 className="text-sm font-semibold">版本历史</h2><ol className="mt-5 space-y-5 border-l border-border pl-5">{[["v2.3", "当前", "4 月 12 日"], ["v2.2", "", "3 月 3 日"], ["v2.1", "", "1 月 18 日"]].map(([version, current, date]) => <li className="relative" key={version}><span className={`absolute -left-[26px] top-1.5 size-2.5 rounded-full ${current ? "bg-primary ring-4 ring-primary/15" : "bg-muted-foreground/40"}`} /><div className="flex gap-2"><span className="text-sm font-semibold">{version}</span>{current ? <span className="rounded-full bg-calm/10 px-1.5 py-0.5 text-[10px] font-medium text-calm">当前</span> : null}</div><p className="mt-1 text-xs text-muted-foreground">{date}</p></li>)}</ol><Button className="mt-5 w-full rounded-xl" size="sm" variant="outline">查看全部版本 <ArrowRight className="size-3.5" aria-hidden="true" /></Button></CardContent></Card><Card className="surface-shadow border-white/70 bg-card/90 py-0"><CardContent className="p-5"><h2 className="text-sm font-semibold">使用范围</h2><dl className="mt-4 space-y-3 text-sm"><div className="flex justify-between"><dt className="text-muted-foreground">仓库</dt><dd className="font-medium">42</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">团队</dt><dd className="font-medium">5</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">开发者</dt><dd className="font-medium">~1,200</dd></div></dl></CardContent></Card></div>
        </section>

        <section className="surface-shadow flex flex-col gap-4 rounded-[22px] border border-white/70 bg-card/90 p-5 md:flex-row md:items-center md:justify-between"><div className="flex items-center gap-3"><span className="flex size-11 items-center justify-center rounded-2xl bg-primary/10 text-primary"><BarChart3 className="size-5" aria-hidden="true" /></span><div><h2 className="font-semibold">发布前先查看影响</h2><p className="mt-1 text-sm text-muted-foreground">基于 42 个近期 Pull Request：6 条新增 critical、3 条可能误报、87% 置信度。</p></div></div><div className="flex shrink-0 gap-2"><Button className="rounded-xl" variant="outline">打开影响预览</Button><Button className="rounded-xl">创建 v2.4 草稿</Button></div></section>
      </div>
    </WorkspaceShell>
  );
}
