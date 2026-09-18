import { ConsoleShell } from "@/components/console/console-shell";

export default async function WorkspaceLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  return <ConsoleShell org={org}>{children}</ConsoleShell>;
}
