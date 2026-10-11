"use client";

import { useRef, useState, type ReactNode, type SyntheticEvent } from "react";
import { CircleAlert } from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useWorkflowText } from "@/components/console/ui-language-context";
import { cn } from "@/lib/utils";

export function HelpHint({ children, className, label }: { children: ReactNode; className?: string; label?: string }) {
  const t = useWorkflowText();
  const helpLabel = label?.trim();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLSpanElement>(null);
  // Ignore a pending hover timer after an explicit click dismissal.
  const dismissedByClick = useRef(false);

  function toggle(event: SyntheticEvent) {
    event.preventDefault();
    event.stopPropagation();
    dismissedByClick.current = open;
    setOpen(!open);
  }

  return (
    <Tooltip delayDuration={180} onOpenChange={(nextOpen) => {
      if (!nextOpen || !dismissedByClick.current) setOpen(nextOpen);
    }} open={open}>
      <TooltipTrigger asChild>
        <span
          aria-expanded={open}
          aria-label={helpLabel ? t("More information about {label}", { label: helpLabel }) : t("More information")}
          className={cn("luminous-focus inline-flex size-7 shrink-0 items-center justify-center rounded-full text-[var(--ls-text-tertiary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-accent)]", className)}
          data-help-hint=""
          onClick={toggle}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") toggle(event);
          }}
          // Preserve hover/focus behavior while making click/tap a toggle,
          // rather than Radix's default pointer-down and click dismissal.
          onPointerDown={(event) => event.preventDefault()}
          onPointerEnter={() => { dismissedByClick.current = false; }}
          onPointerMove={(event) => {
            if (dismissedByClick.current) event.preventDefault();
          }}
          onFocus={() => { dismissedByClick.current = false; }}
          ref={triggerRef}
          // Help remains available inside a disabled fieldset without
          // enabling any of its form controls or publishing actions.
          role="button"
          tabIndex={0}
        >
          <CircleAlert aria-hidden="true" className="size-4" />
        </span>
      </TooltipTrigger>
      <TooltipContent
        className="block max-h-[min(24rem,60vh)] max-w-[min(22rem,calc(100vw-2rem))] overflow-y-auto whitespace-normal px-4 py-3 text-left text-xs leading-6 shadow-lg"
        collisionPadding={16}
        onPointerDownOutside={(event) => {
          // The trigger is outside the portal. Let its click toggle the open
          // state instead of dismissing first and immediately reopening.
          const target = event.detail.originalEvent.target;
          if (target instanceof Node && triggerRef.current?.contains(target)) event.preventDefault();
        }}
        side="bottom"
        sideOffset={8}
      >
        {helpLabel ? <span className="mb-1 block font-semibold">{helpLabel}</span> : null}
        {children}
      </TooltipContent>
    </Tooltip>
  );
}

// The help button is a sibling of the label: clicking it must never change a
// checkbox/select or steal the label's association with its actual input.
export function FieldLabel({ children, help, htmlFor, label }: { children?: ReactNode; help?: ReactNode; htmlFor: string; label: string }) {
  return (
    <div className="mb-2 flex items-center justify-between gap-3 text-xs font-medium text-[var(--ls-text-secondary)]">
      <div className="flex min-w-0 items-center gap-1.5">
        <label htmlFor={htmlFor}>{label}</label>
        {help ? <HelpHint label={label}>{help}</HelpHint> : null}
      </div>
      {children}
    </div>
  );
}
