"use client";

import { useEffect } from "react";

export function FindingFocus({ findingId }: { findingId: string }) {
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const target = document.getElementById(`finding-${findingId}`);
      if (!target) return;
      target.focus({ preventScroll: true });
      target.scrollIntoView({ block: "start" });
    });
    return () => cancelAnimationFrame(frame);
  }, [findingId]);

  return null;
}
