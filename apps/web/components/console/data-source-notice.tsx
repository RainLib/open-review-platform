import { Database, TriangleAlert, Wrench } from "lucide-react";

import type { ConsoleData } from "@/lib/control-api";

export function DataSourceNotice({ data }: { data: ConsoleData }) {
  if (data.source === "live") {
    return (
      <div className="flex items-center gap-2 text-xs text-[var(--ls-success-text)]">
        <span className="size-1.5 rounded-full bg-[var(--ls-success)]" />
        Live control-plane data
      </div>
    );
  }

  const isDemo = data.source === "demo";
  const Icon = isDemo ? Wrench : TriangleAlert;
  const title = isDemo
    ? "Preview data"
    : data.source === "unavailable"
      ? "Control plane unavailable"
      : "Control plane not connected";
  return (
    <div className="flex items-start gap-2.5 rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_8%,transparent)] px-3 py-2.5 text-xs leading-5 text-[var(--ls-warning-text)]">
      <Icon className="mt-0.5 size-3.5 shrink-0 text-[var(--ls-warning)]" />
      <div>
        <span className="font-medium">{title}.</span>{" "}
        {data.detail ??
          (isDemo
            ? "OPEN_REVIEW_CONSOLE_DEMO is enabled; no production data is shown."
            : "Check the server connection configuration.")}
      </div>
    </div>
  );
}

export function EmptyData({
  title,
  detail,
}: {
  title: string;
  detail: string;
}) {
  return (
    <div className="grid min-h-56 place-items-center rounded-[16px] border border-dashed border-[var(--ls-line-strong)] bg-[var(--ls-surface)] p-8 text-center">
      <div>
        <Database className="mx-auto mb-3 size-5 text-[var(--ls-text-tertiary)]" />
        <h2 className="text-sm font-medium text-[var(--ls-text)]">{title}</h2>
        <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-[var(--ls-text-secondary)]">
          {detail}
        </p>
      </div>
    </div>
  );
}
