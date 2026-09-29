"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";

export function CopyEvidenceButton({
  label = "Copy",
  value,
}: {
  label?: string;
  value: string;
}) {
  const [state, setState] = useState<"idle" | "copied" | "error">("idle");

  async function copy() {
    let copied = false;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
        copied = true;
      }
    } catch {
      copied = false;
    }
    if (!copied) {
      const textarea = document.createElement("textarea");
      textarea.value = value;
      textarea.setAttribute("readonly", "");
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      copied = document.execCommand("copy");
      textarea.remove();
    }
    setState(copied ? "copied" : "error");
    window.setTimeout(() => setState("idle"), 1600);
  }

  return (
    <button
      aria-live="polite"
      className="luminous-focus inline-flex h-8 items-center gap-1.5 rounded-[9px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2.5 text-xs font-medium text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] transition hover:text-[var(--ls-text)]"
      onClick={copy}
      type="button"
    >
      {state === "copied" ? <Check className="size-3.5 text-[var(--ls-success-text)]" /> : <Copy className="size-3.5" />}
      {state === "copied" ? "Copied" : state === "error" ? "Copy failed" : label}
    </button>
  );
}
