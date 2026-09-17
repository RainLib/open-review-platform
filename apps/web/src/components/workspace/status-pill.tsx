import { CircleAlert, CircleCheck, Clock3, LoaderCircle } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { TaskState } from "@/lib/demo-data";

const stateConfig: Record<TaskState, { label: string; icon: typeof LoaderCircle; className: string }> = {
  running: {
    label: "审查中",
    icon: LoaderCircle,
    className: "border-primary/10 bg-primary/10 text-primary",
  },
  queued: {
    label: "队列中",
    icon: Clock3,
    className: "border-muted-foreground/10 bg-muted text-muted-foreground",
  },
  completed: {
    label: "已完成",
    icon: CircleCheck,
    className: "border-calm/10 bg-calm/10 text-calm",
  },
  attention: {
    label: "需要处理",
    icon: CircleAlert,
    className: "border-attention/10 bg-attention/10 text-attention",
  },
};

export function StatusPill({ state, className }: { state: TaskState; className?: string }) {
  const { label, icon: Icon, className: stateClassName } = stateConfig[state];

  return (
    <Badge className={cn("h-6 gap-1.5 rounded-full px-2.5 text-[11px]", stateClassName, className)} variant="outline">
      <Icon className={cn("size-3", state === "running" && "animate-spin")} aria-hidden="true" />
      {label}
    </Badge>
  );
}
