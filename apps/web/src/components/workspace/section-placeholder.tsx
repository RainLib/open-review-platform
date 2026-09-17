import { ArrowRight, Blocks, Cable, FolderGit2, Settings2, Workflow } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { WorkspaceShell } from "@/components/workspace/app-shell";

const sections = {
  reviews: { eyebrow: "审查", title: "审查活动中心", description: "跨仓库追踪 Pull Request、发现和发布状态。", icon: Workflow, next: "下一个可交付切片会把 GitHub 与 GitLab 审查事件接到统一的任务入口。" },
  repos: { eyebrow: "仓库", title: "仓库与范围", description: "为每个工程设置审查范围、风险等级和默认策略。", icon: FolderGit2, next: "后续会在此展示安装状态、分支保护和仓库级策略覆盖。" },
  connect: { eyebrow: "连接", title: "连接中心", description: "集中管理 GitHub App、GitLab App 与回调健康状态。", icon: Cable, next: "后续会接入安装向导、密钥轮换和 webhook 投递诊断。" },
  settings: { eyebrow: "设置", title: "组织设置", description: "管理成员、预算、留存与企业级审查偏好。", icon: Settings2, next: "后续会接入 Casdoor 身份映射与组织级治理设置。" },
} as const;

type SectionKey = keyof typeof sections;

export function isWorkspaceSection(section: string): section is SectionKey {
  return section in sections;
}

export function SectionPlaceholder({ organization, section }: { organization: string; section: SectionKey }) {
  const definition = sections[section];
  const Icon = definition.icon;

  return (
    <WorkspaceShell organization={organization}>
      <div className="flex min-h-[calc(100vh-11rem)] items-center justify-center py-8">
        <Card className="surface-shadow w-full max-w-2xl border-white/70 bg-card/90 py-0">
          <CardContent className="p-7 md:p-10">
            <span className="flex size-14 items-center justify-center rounded-2xl bg-primary/10 text-primary"><Icon className="size-6" aria-hidden="true" /></span>
            <p className="mt-7 text-sm font-medium text-primary">{definition.eyebrow}</p>
            <h1 className="mt-2 text-3xl font-semibold tracking-[-0.04em]">{definition.title}</h1>
            <p className="mt-3 max-w-xl text-base leading-7 text-muted-foreground">{definition.description}</p>
            <div className="mt-7 rounded-2xl bg-surface-tint p-4"><div className="flex gap-3"><Blocks className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" /><p className="text-sm leading-6 text-muted-foreground">{definition.next}</p></div></div>
            <div className="mt-7 flex flex-wrap gap-3"><Button asChild className="rounded-xl"><a href={`/${organization}/home`}>返回工作台 <ArrowRight className="size-4" aria-hidden="true" /></a></Button><Button asChild className="rounded-xl" variant="outline"><a href={`/${organization}/tasks`}>查看任务</a></Button></div>
          </CardContent>
        </Card>
      </div>
    </WorkspaceShell>
  );
}
