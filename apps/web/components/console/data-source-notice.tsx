import { Database, TriangleAlert, Wrench } from "lucide-react";

import type { ConsoleData } from "@/lib/control-api";

export function DataSourceNotice({ data }: { data: ConsoleData }) {
  if (data.source === "live") {
    return (
      <div className="flex items-center gap-2 text-xs text-emerald-200/80">
        <span className="size-1.5 rounded-full bg-emerald-300 shadow-console-success" />
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
    <div className="flex items-start gap-2.5 rounded-xl border border-amber-300/15 bg-amber-300/[0.045] px-3 py-2.5 text-xs leading-5 text-amber-100/75">
      <Icon className="mt-0.5 size-3.5 shrink-0 text-amber-300" />
      <div>
        <span className="font-medium text-amber-100">{title}.</span>{" "}
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
    <div className="grid min-h-56 place-items-center rounded-2xl border border-dashed border-white/10 bg-white/[0.015] p-8 text-center">
      <div>
        <Database className="mx-auto mb-3 size-5 text-zinc-600" />
        <h2 className="text-sm font-medium text-zinc-200">{title}</h2>
        <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-zinc-500">
          {detail}
        </p>
      </div>
    </div>
  );
}
