"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

type TabStateRouterProps = {
  children: React.ReactNode;
  className?: string;
  label: string;
  dismissSelection?: boolean;
};

// URL-backed tabs remain ordinary links so refresh, history, and copied URLs
// preserve the selected view. This wrapper adds the keyboard behavior expected
// from a tab list without turning an async server-rendered view into client
// state that can drift from its evidence URL.
export function TabStateRouter({
  children,
  className,
  label,
  dismissSelection = false,
}: TabStateRouterProps) {
  const tabList = useRef<HTMLElement>(null);
  const router = useRouter();

  useEffect(() => {
    if (!dismissSelection) return;
    tabList.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus();
    const url = new URL(window.location.href);
    url.searchParams.delete("selected");
    router.replace(`${url.pathname}${url.search}`, { scroll: false });
  }, [dismissSelection, router]);

  function moveFocus(current: HTMLElement, direction: 1 | -1) {
    const tabs = Array.from(
      tabList.current?.querySelectorAll<HTMLElement>('[role="tab"]') ?? [],
    ).filter((tab) => tab.getAttribute("aria-disabled") !== "true");
    const currentIndex = tabs.indexOf(current);
    if (currentIndex < 0 || tabs.length === 0) return;
    tabs[(currentIndex + direction + tabs.length) % tabs.length]?.focus();
  }

  function onKeyDown(event: React.KeyboardEvent<HTMLElement>) {
    const current = event.target instanceof HTMLElement
      ? event.target.closest<HTMLElement>('[role="tab"]')
      : null;
    if (!current) return;

    switch (event.key) {
      case "ArrowRight":
      case "ArrowDown":
        event.preventDefault();
        moveFocus(current, 1);
        return;
      case "ArrowLeft":
      case "ArrowUp":
        event.preventDefault();
        moveFocus(current, -1);
        return;
      case "Home":
        event.preventDefault();
        tabList.current?.querySelector<HTMLElement>('[role="tab"]')?.focus();
        return;
      case "End": {
        event.preventDefault();
        const tabs = tabList.current?.querySelectorAll<HTMLElement>('[role="tab"]');
        tabs?.item(tabs.length - 1)?.focus();
        return;
      }
      case " ":
      case "Space":
      case "Spacebar":
        event.preventDefault();
        current.click();
        return;
      default:
        return;
    }
  }

  return (
    <nav
      aria-label={label}
      className={`min-w-0 max-w-full ${className ?? ""}`}
      onKeyDown={onKeyDown}
      ref={tabList}
      role="tablist"
    >
      {children}
    </nav>
  );
}
