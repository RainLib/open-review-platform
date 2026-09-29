import type { RunState } from "@/lib/control-api";
import type { UiLanguage } from "@/lib/ui-language";

export function formatTime(value?: string, language: UiLanguage = "en") {
  if (!value) return "—";
  return new Intl.DateTimeFormat(language, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
		// Server components and hydrated client components must render the same
		// evidence timestamp regardless of the host or browser locale.
		timeZone: "UTC",
		timeZoneName: "short",
  }).format(new Date(value));
}

export function shortSHA(value: string) {
  return value.slice(0, 8);
}

export function displayRunState(state: RunState, language: UiLanguage = "en") {
  if (language === "zh-CN") {
    return ({ acknowledged: "已确认", admitted: "已准入", preparing: "准备中", analyzing: "分析中", normalizing: "整理中", publishing: "发布中", completed: "已完成", failed: "失败", cancelled: "已取消", superseded: "已被替代", needs_attention: "需人工处理" } satisfies Record<RunState, string>)[state];
  }
  return state.replaceAll("_", " ");
}

export function isActiveRun(state: RunState) {
  return [
    "acknowledged",
    "admitted",
    "preparing",
    "analyzing",
    "normalizing",
    "publishing",
  ].includes(state);
}

export function isAttentionRun(state: RunState) {
  return state === "failed" || state === "needs_attention";
}
