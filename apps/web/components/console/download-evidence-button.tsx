"use client";

import { Download } from "lucide-react";

export function DownloadEvidenceButton({
  fileName,
  value,
}: {
  fileName: string;
  value: string;
}) {
  function download() {
    const blob = new Blob([value], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.download = fileName;
    anchor.href = url;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <button
      className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text-secondary)] shadow-[var(--ls-shadow-control)] hover:text-[var(--ls-text)]"
      onClick={download}
      type="button"
    >
      <Download className="size-4" />
      Download evidence
    </button>
  );
}
