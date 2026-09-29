"use client";

import * as React from "react";
import { Dialog as Primitive } from "radix-ui";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

export const Dialog = Primitive.Root;
export const DialogTrigger = Primitive.Trigger;
export const DialogClose = Primitive.Close;
export const DialogTitle = Primitive.Title;
export const DialogDescription = Primitive.Description;

export function DialogContent({ children, className, ...props }: React.ComponentProps<typeof Primitive.Content>) {
  return <Primitive.Portal>
    <Primitive.Overlay className="fixed inset-0 z-50 bg-black/25 backdrop-blur-sm data-[state=open]:animate-in data-[state=open]:fade-in-0 motion-reduce:animate-none" />
    <Primitive.Content className={cn("fixed inset-0 z-50 flex flex-col overflow-y-auto bg-[var(--ls-surface)] p-5 text-[var(--ls-text)] shadow-[var(--ls-shadow-float)] outline-none sm:inset-auto sm:left-1/2 sm:top-1/2 sm:max-h-[85dvh] sm:w-[calc(100%-3rem)] sm:max-w-2xl sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-[24px] sm:border sm:border-[var(--ls-line-strong)] sm:p-6", className)} {...props}>
      {children}
      <Primitive.Close aria-label="Close dialog" className="luminous-focus absolute right-4 top-4 grid size-8 place-items-center rounded-[9px] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]"><X className="size-4" /></Primitive.Close>
    </Primitive.Content>
  </Primitive.Portal>;
}
