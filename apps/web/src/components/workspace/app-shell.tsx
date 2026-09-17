"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import {
  ArrowRight,
  Bot,
  FolderGit2,
  Gauge,
  Home,
  Keyboard,
  MoreHorizontal,
  Network,
  Search,
  Settings,
  ShieldCheck,
  Sparkles,
  Workflow,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { Separator } from "@/components/ui/separator";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

type WorkspaceActions = { openCommand: () => void };
const WorkspaceActionsContext = createContext<WorkspaceActions | null>(null);

export function useWorkspaceActions() {
  const context = useContext(WorkspaceActionsContext);
  if (!context) {
    throw new Error("useWorkspaceActions must be used inside WorkspaceShell");
  }
  return context;
}

const primaryNavigation = [
  { label: "工作台", href: "home", icon: Home },
  { label: "审查", href: "reviews", icon: Workflow },
  { label: "任务", href: "tasks", icon: Gauge },
  { label: "策略", href: "rules", icon: ShieldCheck },
  { label: "仓库", href: "repos", icon: FolderGit2 },
  { label: "连接", href: "connect", icon: Network },
];

export function WorkspaceShell({ children, organization }: { children: ReactNode; organization: string }) {
  const pathname = usePathname();
  const router = useRouter();
  const [commandOpen, setCommandOpen] = useState(false);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const closeCommand = () => setCommandOpen(false);

  return (
    <WorkspaceActionsContext.Provider value={{ openCommand: () => setCommandOpen(true) }}>
      <main className="min-h-screen bg-background p-3 text-foreground md:p-4">
        <div className="mx-auto grid min-h-[calc(100vh-1.5rem)] max-w-[1680px] grid-cols-[76px_minmax(0,1fr)] gap-3 md:gap-4">
          <aside className="surface-shadow sticky top-3 flex h-[calc(100vh-1.5rem)] flex-col rounded-[28px] bg-rail px-2 py-3 text-rail-foreground md:top-4 md:h-[calc(100vh-2rem)]">
            <Link aria-label="Open Review 工作台" className="mb-5 flex size-12 items-center justify-center rounded-2xl bg-white/10 text-white" href={`/${organization}/home`}>
              <Sparkles className="size-5" aria-hidden="true" />
            </Link>
            <nav aria-label="主导航" className="flex flex-1 flex-col items-center gap-2">
              {primaryNavigation.map(({ label, href, icon: Icon }) => {
                const active = pathname.startsWith(`/${organization}/${href}`);
                return (
                  <Tooltip key={href}>
                    <TooltipTrigger asChild>
                      <Link
                        aria-current={active ? "page" : undefined}
                        className={cn(
                          "flex size-11 items-center justify-center rounded-2xl text-white/65 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/70",
                          active && "bg-primary text-primary-foreground shadow-lg shadow-primary/30",
                        )}
                        href={`/${organization}/${href}`}
                      >
                        <Icon className="size-[19px]" aria-hidden="true" />
                      </Link>
                    </TooltipTrigger>
                    <TooltipContent side="right">{label}</TooltipContent>
                  </Tooltip>
                );
              })}
            </nav>
            <div className="space-y-2">
              <Separator className="bg-white/10" />
              <Tooltip>
                <TooltipTrigger asChild>
                  <Link className="flex size-11 items-center justify-center rounded-2xl text-white/65 hover:bg-white/10 hover:text-white" href={`/${organization}/settings`}>
                    <Settings className="size-[19px]" aria-hidden="true" />
                  </Link>
                </TooltipTrigger>
                <TooltipContent side="right">设置</TooltipContent>
              </Tooltip>
            </div>
          </aside>

          <section className="min-w-0">
            <header className="surface-shadow mb-4 flex min-h-14 items-center justify-between rounded-[22px] border border-white/60 bg-card/85 px-3 backdrop-blur-xl md:px-5">
              <div className="flex min-w-0 items-center gap-2 text-sm font-medium">
                <span className="truncate">{organization === "acme" ? "Acme Engineering" : organization}</span>
                <span className="text-muted-foreground">/</span>
                <span className="hidden items-center gap-1 text-muted-foreground sm:flex"><span className="size-1.5 rounded-full bg-live" />Production</span>
              </div>
              <div className="flex items-center gap-1.5 md:gap-2">
                <Button className="hidden w-[270px] justify-start rounded-xl bg-muted/70 text-muted-foreground hover:bg-muted md:flex" onClick={() => setCommandOpen(true)} variant="ghost">
                  <Search className="size-4" aria-hidden="true" />
                  <span className="flex-1 text-left">询问 Open Review 或跳转…</span>
                  <kbd className="rounded-md border border-border bg-background px-1.5 py-0.5 text-[10px]">⌘K</kbd>
                </Button>
                <Button aria-label="打开命令面板" className="md:hidden" onClick={() => setCommandOpen(true)} size="icon" variant="ghost">
                  <Search className="size-4" aria-hidden="true" />
                </Button>
                <Button aria-label="更多账户操作" size="icon" variant="ghost"><MoreHorizontal className="size-4" aria-hidden="true" /></Button>
                <span className="flex size-8 items-center justify-center rounded-full bg-gradient-to-br from-violet-500 to-sky-400 text-xs font-semibold text-white">MK</span>
              </div>
            </header>
            {children}
          </section>
        </div>
      </main>

      <CommandDialog onOpenChange={setCommandOpen} open={commandOpen} title="Open Review 命令面板">
        <Command>
          <CommandInput placeholder="搜索任务、仓库、规则或动作…" />
          <CommandList>
            <CommandEmpty>没有匹配的结果。</CommandEmpty>
            <CommandGroup heading="快速操作">
              <CommandItem onSelect={() => { closeCommand(); router.push(`/${organization}/tasks`); }}><Sparkles className="size-4 text-primary" />开始一次审查</CommandItem>
              <CommandItem onSelect={() => { closeCommand(); router.push(`/${organization}/rules`); }}><ShieldCheck className="size-4 text-primary" />创建规则草稿</CommandItem>
            </CommandGroup>
            <CommandSeparator />
            <CommandGroup heading="最近访问">
              <CommandItem onSelect={() => { closeCommand(); router.push(`/${organization}/tasks/ORP-1842`); }}><Bot className="size-4 text-live" />ORP-1842 · 支付重试审查<ArrowRight className="ml-auto size-3" /></CommandItem>
              <CommandItem onSelect={() => { closeCommand(); router.push(`/${organization}/rules`); }}><Keyboard className="size-4 text-primary" />Secure API Baseline · v2.3</CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </CommandDialog>
    </WorkspaceActionsContext.Provider>
  );
}
