"use client";

import { useSyncExternalStore } from "react";
import { Monitor, Moon, Sun } from "lucide-react";

import { cn } from "@/lib/utils";

type ThemePreference = "system" | "light" | "dark";

const themeChangeEvent = "open-review-theme-change";

function readTheme(): ThemePreference {
  const stored = window.localStorage.getItem("open-review-theme");
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : "light";
}

function subscribeToTheme(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange);
  window.addEventListener(themeChangeEvent, onStoreChange);
  return () => {
    window.removeEventListener("storage", onStoreChange);
    window.removeEventListener(themeChangeEvent, onStoreChange);
  };
}

function nextTheme(theme: ThemePreference): ThemePreference {
  return theme === "light" ? "dark" : theme === "dark" ? "system" : "light";
}

export function LuminousPublicFrame({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  const theme = useSyncExternalStore(subscribeToTheme, readTheme, () => "light" as ThemePreference);
  return (
    <div className={cn("luminous-console luminous-public", `luminous-theme-${theme}`, className)}>
      {children}
    </div>
  );
}

export function PublicThemeToggle() {
  const theme = useSyncExternalStore(subscribeToTheme, readTheme, () => "light" as ThemePreference);
  const Icon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  function cycleTheme() {
    const next = nextTheme(theme);
    window.localStorage.setItem("open-review-theme", next);
    window.dispatchEvent(new Event(themeChangeEvent));
  }

  return (
    <button
      aria-label={`Theme: ${theme}. Activate to change theme.`}
      className="luminous-focus grid size-9 place-items-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]"
      onClick={cycleTheme}
      title={`Theme: ${theme}`}
      type="button"
    >
      <Icon className="size-4" />
    </button>
  );
}
