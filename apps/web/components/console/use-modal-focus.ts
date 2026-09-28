"use client";

import { useEffect, useRef, type RefObject } from "react";

const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export function useModalFocus<T extends HTMLElement>(
  onClose: () => void,
  {
    active = true,
    closeOnEscape = true,
    returnFocusRef,
  }: {
    active?: boolean;
    closeOnEscape?: boolean;
    returnFocusRef?: RefObject<HTMLElement | null>;
  } = {},
) {
  const containerRef = useRef<T>(null);
  const closeRef = useRef(onClose);

  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    if (!active) return;
    const previousFocus = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    const returnTarget = returnFocusRef?.current ?? previousFocus;
    const container = containerRef.current;
    if (!container) return;

    function focusableElements() {
      return Array.from(
        container?.querySelectorAll<HTMLElement>(focusableSelector) ?? [],
      ).filter((element) =>
        element.getClientRects().length > 0 &&
        // Descendants of a disabled fieldset do not carry a disabled
        // attribute, but :disabled still matches them. Attempting to focus
        // one silently fails and traps Tab on the previous control.
        !element.matches(":disabled") &&
        !element.closest("[inert], [aria-hidden='true']"),
      );
    }

    const initial = container.querySelector<HTMLElement>(
      "[data-dialog-initial-focus]",
    ) ?? focusableElements()[0];
    const focusFrame = window.requestAnimationFrame(() => initial?.focus());

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape" && closeOnEscape) {
        event.preventDefault();
        closeRef.current();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = focusableElements();
      if (!focusable.length) {
        event.preventDefault();
        return;
      }
      const currentIndex = focusable.indexOf(document.activeElement as HTMLElement);
      const nextIndex = currentIndex < 0
        ? event.shiftKey ? focusable.length - 1 : 0
        : event.shiftKey
          ? currentIndex === 0 ? focusable.length - 1 : currentIndex - 1
          : currentIndex === focusable.length - 1 ? 0 : currentIndex + 1;
      event.preventDefault();
      focusable[nextIndex]?.focus();
    }

    container.addEventListener("keydown", onKeyDown);
    return () => {
      window.cancelAnimationFrame(focusFrame);
      container.removeEventListener("keydown", onKeyDown);
      window.requestAnimationFrame(() => returnTarget?.focus());
    };
  }, [active, closeOnEscape, returnFocusRef]);

  return containerRef;
}
