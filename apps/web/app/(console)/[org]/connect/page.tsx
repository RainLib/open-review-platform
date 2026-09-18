import {
  CheckCircle2,
  Circle,
  ExternalLink,
  KeyRound,
  ShieldCheck,
} from "lucide-react";

import { DataSourceNotice } from "@/components/console/data-source-notice";
import { ConnectionManager } from "@/components/console/connection-manager";
import {
  ImplementationNotice,
  PageTitle,
} from "@/components/console/console-shell";
import { getConsoleData } from "@/lib/control-api";

const steps = [
  {
    title: "Identity",
    detail:
      "Casdoor is the configured OIDC verifier for the control plane. The browser authorization-code and session bridge is the next authenticated-console increment.",
    complete: false,
  },
  {
    title: "Control plane",
    detail:
      "Read the review-run and rule-set APIs server-side. For local development, use CONTROL_API_URL and CONTROL_API_DEVELOPMENT_SUBJECT.",
    complete: false,
  },
  {
    title: "Source providers",
    detail:
      "Register GitHub or GitLab installations through the control plane. GitHub uses its deployment-mounted App key; GitLab uses its deployment credential.",
    complete: false,
  },
  {
    title: "Governance",
    detail:
      "Create rule versions, request approval, publish, then bind their immutable version to a repository scope.",
    complete: false,
  },
];

export default async function ConnectPage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  const data = await getConsoleData(org);
  const live = data.source === "live";
  return (
    <div className="space-y-7">
      <div className="flex flex-col justify-between gap-5 lg:flex-row lg:items-end">
        <PageTitle
          eyebrow="Set up"
          title="Connections and trust"
          description="Open Review deliberately keeps credential custody and repository privileges outside the browser. Each connection has a separate trust boundary."
        />
        <div className="lg:w-[360px]">
          <DataSourceNotice data={data} />
        </div>
      </div>
      <ImplementationNotice>
        Connection writes are available through the local development bridge.
        The browser never receives provider tokens or a GitHub App private key;
        production writes remain disabled until the Casdoor session bridge is
        configured.
      </ImplementationNotice>
      <ConnectionManager
        enabled={live}
        installations={data.installations}
        org={org}
      />
      <div className="grid gap-3 xl:grid-cols-[minmax(0,1.35fr)_minmax(280px,0.65fr)]">
        <div className="rounded-2xl border border-white/[0.075] bg-console-surface p-5 sm:p-6">
          <p className="text-sm font-medium text-zinc-100">
            Connection sequence
          </p>
          <div className="mt-6 space-y-0">
            {steps.map((step, index) => (
              <div
                className="relative grid grid-cols-[28px_minmax(0,1fr)] gap-3 pb-7 last:pb-0"
                key={step.title}
              >
                {index !== steps.length - 1 ? (
                  <div className="absolute left-[11px] top-6 h-[calc(100%-10px)] w-px bg-white/[0.08]" />
                ) : null}
                {live && index === 1 ? (
                  <CheckCircle2 className="mt-0.5 size-5 text-emerald-300" />
                ) : (
                  <Circle className="mt-0.5 size-5 text-zinc-600" />
                )}
                <div>
                  <h2 className="text-sm font-medium text-zinc-200">
                    {step.title}
                  </h2>
                  <p className="mt-1.5 max-w-2xl text-sm leading-6 text-zinc-500">
                    {step.detail}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </div>
        <aside className="rounded-2xl border border-cyan-300/10 bg-[radial-gradient(ellipse_at_top,var(--console-accent-glow),transparent_65%),var(--console-surface)] p-5">
          <KeyRound className="size-5 text-cyan-300" />
          <h2 className="mt-4 text-sm font-medium text-zinc-100">
            Credential custody
          </h2>
          <p className="mt-2 text-sm leading-6 text-zinc-400">
            The console reads a control-plane API. It never receives the
            provider credential reference stored against an installation.
          </p>
          <div className="mt-6 border-t border-white/[0.06] pt-4">
            <div className="flex gap-2 text-xs leading-5 text-zinc-500">
              <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-emerald-300" />
              Policy is resolved when a run is admitted, not from the pull
              request head.
            </div>
          </div>
          <a
            className="mt-5 inline-flex items-center gap-1.5 text-xs font-medium text-cyan-200 hover:text-cyan-100"
            href="https://github.com/RainLib/open-review-platform/blob/main/README.md"
            rel="noreferrer"
            target="_blank"
          >
            Deployment reference <ExternalLink className="size-3.5" />
          </a>
        </aside>
      </div>
    </div>
  );
}
