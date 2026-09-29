import { notFound, redirect } from "next/navigation";

import { setupStepFromSegment } from "@/lib/setup-route";
import { renderSetupPage, type SetupSearchParams } from "../_render";

function onlyValue(value: string | string[] | undefined) {
  return typeof value === "string" ? value : undefined;
}

export default async function ProviderSetupPage({
  params,
  searchParams,
}: {
  params: Promise<{ provider: string }>;
  searchParams: Promise<SetupSearchParams & {
    installation_id?: string | string[];
    state?: string | string[];
  }>;
}) {
  const { provider } = await params;
  const query = await searchParams;
  if (provider === "github" && (onlyValue(query.installation_id) || onlyValue(query.state))) {
    const callback = new URLSearchParams();
    const installationID = onlyValue(query.installation_id);
    const state = onlyValue(query.state);
    if (installationID) callback.set("installation_id", installationID);
    if (state) callback.set("state", state);
    redirect(`/api/setup/github/complete?${callback.toString()}`);
  }
  const step = setupStepFromSegment(provider);
  if (provider === "rules") return renderSetupPage(query);
  if (!step) notFound();
  // The GitHub App callback above keeps its established provider handoff.
  // Every other setup URL goes through the same session, workspace and signed
  // receipt checks as /setup; a path never grants installation authority.
  const selectedProvider = provider === "github" || provider === "gitlab"
    ? provider
    : undefined;
  return renderSetupPage(query, step, selectedProvider);
}
