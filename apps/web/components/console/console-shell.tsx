import type { AccessibleWorkspace } from "@/lib/control-api";
import { LuminousConsoleShell } from "@/components/console/luminous-console-shell";

export function ConsoleShell({
  children,
  org,
  preview = false,
  workspaces,
}: {
  children: React.ReactNode;
  org: string;
  preview?: boolean;
  workspaces: AccessibleWorkspace[];
}) {
  // The workspace layout is the sole shell boundary. Every current and future
  // workspace route must share the same navigation, theme and main landmark.
  return (
    <LuminousConsoleShell org={org} preview={preview} workspaces={workspaces}>
      {children}
    </LuminousConsoleShell>
  );
}
