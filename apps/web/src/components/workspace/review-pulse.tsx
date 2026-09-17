import { Activity, MoreHorizontal, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { People } from "@/components/workspace/people";

const stages = [
  { label: "已接收", count: 8, position: "left-[7%] top-[57%]", tone: "bg-violet-400" },
  { label: "理解变更", count: 6, position: "left-[33%] top-[38%]", tone: "bg-primary" },
  { label: "审查中", count: 12, position: "left-[61%] top-[21%]", tone: "bg-primary" },
  { label: "发布中", count: 4, position: "right-[7%] top-[42%]", tone: "bg-calm" },
];

export function ReviewPulse() {
  return (
    <Card className="surface-shadow relative min-h-[310px] border-white/70 bg-card/88 py-0">
      <CardHeader className="relative z-10 px-6 pt-6 md:px-7">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <CardTitle className="text-xl tracking-tight">审查脉冲</CardTitle>
            <span className="flex items-center gap-2 text-sm text-muted-foreground"><span className="size-2 rounded-full bg-live" />12 个正在运行</span>
          </div>
          <Button aria-label="审查脉冲选项" size="icon-sm" variant="ghost"><MoreHorizontal className="size-4" aria-hidden="true" /></Button>
        </div>
        <p className="mt-1 text-sm text-muted-foreground">跨仓库实时审查流，而不是静态健康指标。</p>
      </CardHeader>
      <CardContent className="relative min-h-[232px] overflow-hidden px-4 pb-5 md:px-7">
        <div aria-hidden="true" className="subtle-grid absolute inset-x-0 top-7 h-[145px] opacity-50" />
        <svg aria-hidden="true" className="absolute inset-x-0 top-8 h-[150px] w-full" fill="none" preserveAspectRatio="none" viewBox="0 0 800 160">
          <defs>
            <linearGradient id="pulse-line" x1="0" x2="800" y1="0" y2="0" gradientUnits="userSpaceOnUse">
              <stop stopColor="#B8A5FF" />
              <stop offset="0.48" stopColor="#4F46E5" />
              <stop offset="1" stopColor="#44D3A6" />
            </linearGradient>
            <linearGradient id="pulse-fill" x1="0" x2="0" y1="25" y2="160" gradientUnits="userSpaceOnUse">
              <stop stopColor="#8B7BFF" stopOpacity="0.16" />
              <stop offset="1" stopColor="#8B7BFF" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d="M0 110C66 110 92 52 160 80C234 111 280 121 350 87C414 56 466 14 535 55C617 103 681 109 800 62V160H0V110Z" fill="url(#pulse-fill)" />
          <path d="M0 110C66 110 92 52 160 80C234 111 280 121 350 87C414 56 466 14 535 55C617 103 681 109 800 62" stroke="url(#pulse-line)" strokeLinecap="round" strokeWidth="3" />
        </svg>
        {stages.map((stage) => (
          <div className={`absolute ${stage.position} z-10 -translate-x-1/2`} key={stage.label}>
            <span className={`mx-auto block size-3 rounded-full ring-8 ring-background/70 ${stage.tone}`} />
            <div className="mt-3 text-center">
              <p className="text-xs font-medium text-foreground">{stage.label}</p>
              <p className="mt-0.5 text-sm font-semibold tabular-nums">{stage.count}</p>
            </div>
          </div>
        ))}
        <div className="absolute inset-x-6 bottom-0 flex items-end justify-between gap-4 border-t border-border/70 pt-4 md:inset-x-7">
          <div className="flex items-center gap-3">
            <People people={["MK", "AP", "LD", "JR", "YC"]} />
            <p className="hidden text-xs leading-5 text-muted-foreground sm:block">9 个仓库正在协作<br />AI 与人工审查者同步工作</p>
          </div>
          <div className="hidden items-center gap-2 border-l border-border pl-5 text-xs text-muted-foreground md:flex">
            <Sparkles className="size-4 text-live" aria-hidden="true" />
            <span>队列平稳，未发现系统异常。</span>
          </div>
          <Activity className="size-4 text-primary md:hidden" aria-label="队列平稳" />
        </div>
      </CardContent>
    </Card>
  );
}
