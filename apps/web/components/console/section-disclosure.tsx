"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

// Keep forms mounted so inspecting another section never discards a draft.
// Existing deep links also open their containing section before scrolling.
export function SectionDisclosure({ title, description, count, children, id, defaultOpen = false, open, onOpenChange, className, bodyClassName }: {
  title: string;
  description?: string;
  count?: ReactNode;
  children: ReactNode;
  id?: string;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
  bodyClassName?: string;
}) {
  const element = useRef<HTMLDetailsElement>(null);
  const [expanded, setExpanded] = useState(defaultOpen);
  const isOpen = open ?? expanded;

  useEffect(() => {
    function revealAnchor(hash = window.location.hash) {
      let anchor: string;
      try { anchor = decodeURIComponent(hash.slice(1)); } catch { return; }
      if (!anchor) return;
      const target = document.getElementById(anchor);
      if (!target || !element.current?.contains(target)) return;
      setExpanded(true);
      onOpenChange?.(true);
      window.requestAnimationFrame(() => target.scrollIntoView({ block: "start" }));
    }
    function onHashChange() { revealAnchor(); }
    function onAnchorClick(event: MouseEvent) {
      if (event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>("a[href]") : null;
      if (!link) return;
      const url = new URL(link.href, window.location.href);
      if (url.origin !== window.location.origin || url.pathname !== window.location.pathname || url.search !== window.location.search) return;
      revealAnchor(url.hash);
    }
    revealAnchor();
    window.addEventListener("hashchange", onHashChange);
    window.addEventListener("popstate", onHashChange);
    // Next's Link updates history without emitting hashchange.
    document.addEventListener("click", onAnchorClick, true);
    return () => {
      window.removeEventListener("hashchange", onHashChange);
      window.removeEventListener("popstate", onHashChange);
      document.removeEventListener("click", onAnchorClick, true);
    };
  }, [onOpenChange]);

  return (
    <details ref={element} id={id} open={isOpen} onToggle={event => {
      const next = event.currentTarget.open;
      if (next === isOpen) return;
      setExpanded(next);
      onOpenChange?.(next);
    }} className={cn("group/disclosure min-w-0 scroll-mt-24 rounded-xl border border-[var(--ls-line)] bg-[var(--ls-surface)]", className)}>
      <summary className="luminous-focus flex min-h-14 cursor-pointer list-none items-center gap-3 rounded-xl px-4 py-3 hover:bg-[var(--ls-surface-muted)] sm:px-5 [&::-webkit-details-marker]:hidden">
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-semibold text-[var(--ls-text)]">{title}</span>
          {description ? <span className="mt-1 hidden text-xs leading-5 text-[var(--ls-text-secondary)] sm:block group-open/disclosure:block">{description}</span> : null}
        </span>
        {count !== undefined ? <span className="shrink-0 rounded-md bg-[var(--ls-surface-muted)] px-2 py-1 text-xs tabular-nums text-[var(--ls-text-secondary)]">{count}</span> : null}
        <ChevronDown aria-hidden="true" className="size-4 shrink-0 text-[var(--ls-text-tertiary)] transition-transform group-open/disclosure:rotate-180" />
      </summary>
      <div className={cn("min-w-0 border-t border-[var(--ls-line)] p-4 sm:p-5", bodyClassName)}>{children}</div>
    </details>
  );
}
