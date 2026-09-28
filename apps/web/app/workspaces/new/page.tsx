import Link from "next/link";
import { redirect } from "next/navigation";
import {
  ArrowLeft,
  Building2,
  CircleHelp,
  Cloud,
  MapPin,
  ShieldCheck,
} from "lucide-react";

import { CreateWorkspaceForm } from "@/components/onboarding/create-workspace-form";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { ProductMark } from "@/components/marketing/marketing-shell";
import { getDeploymentProfile } from "@/lib/deployment-profile";
import { requireConsoleSession } from "@/lib/auth/session";
import { sessionRenewalPath } from "@/lib/auth/safe-path";
import { probeConsoleSession } from "@/lib/control-api";

export default async function NewWorkspacePage() {
  await requireConsoleSession("/workspaces/new");
  const sessionState = await probeConsoleSession();
  if (sessionState === "rejected") redirect(sessionRenewalPath("/workspaces/new"));
  const deployment = getDeploymentProfile();

  return (
    <LuminousPublicFrame>
      <main className="min-h-screen p-3 sm:p-5">
        <div className="mx-auto grid min-h-[calc(100vh-1.5rem)] max-w-[1480px] overflow-hidden rounded-[28px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] shadow-[var(--ls-shadow-float)] lg:grid-cols-[220px_minmax(0,1fr)]">
          <aside className="hidden border-r border-[var(--ls-line)] bg-[color:color-mix(in_srgb,var(--ls-surface)_92%,var(--ls-accent-soft))] px-4 py-6 lg:flex lg:flex-col">
            <ProductMark variant="luminous" />
            <ol aria-label="Create workspace steps" className="mt-12 space-y-2">
              {([
                ["1", "Basic information", true],
                ["2", "Deployment boundary", false],
                ["3", "Connect source control", false],
                ["4", "Review configuration", false],
              ] as const).map(([step, label, active]) => (
                <li className={`flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-sm ${active ? "bg-[var(--ls-accent-soft)] font-medium text-[var(--ls-accent)]" : "text-[var(--ls-text-tertiary)]"}`} key={step}>
                  <span className={`grid size-5 place-items-center rounded-full border text-[10px] ${active ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white" : "border-[var(--ls-line-strong)]"}`}>{step}</span>
                  {label}
                </li>
              ))}
            </ol>
            <div className="mt-auto rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-3.5">
              <ShieldCheck className="size-4 text-[var(--ls-accent)]" />
              <p className="mt-2 text-xs font-semibold text-[var(--ls-text)]">Owner by default</p>
              <p className="mt-1 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">The creator becomes the first workspace owner and establishes the initial audit boundary.</p>
            </div>
          </aside>

          <section className="min-w-0">
            <header className="flex h-16 items-center justify-between border-b border-[var(--ls-line)] px-5 sm:px-7">
              <div className="flex items-center gap-3">
                <Link className="luminous-focus inline-flex size-9 items-center justify-center rounded-[10px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" href="/workspaces" title="Back to workspaces">
                  <ArrowLeft className="size-4" />
                </Link>
                <div className="lg:hidden"><ProductMark compact variant="luminous" /></div>
                <span className="hidden text-sm font-medium text-[var(--ls-text-secondary)] sm:inline">Workspace directory</span>
              </div>
              <div className="flex items-center gap-2"><PublicThemeToggle /><span className="grid size-8 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-xs font-semibold text-[var(--ls-accent)]">RL</span></div>
            </header>

            <div className="mx-auto grid max-w-5xl gap-10 px-5 py-8 sm:px-9 sm:py-12 lg:grid-cols-[minmax(0,.72fr)_minmax(420px,1fr)] lg:items-start">
              <div className="pt-2">
                <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><Building2 className="size-5" /></span>
                <p className="mt-7 text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Basic information</p>
                <h1 className="mt-3 text-4xl font-semibold tracking-[-0.06em] text-[var(--ls-text)]">Create a new workspace.</h1>
                <p className="mt-5 max-w-sm text-sm leading-7 text-[var(--ls-text-secondary)]">Start with the organization boundary. Git provider access, review policy, and audit history are connected in the next steps.</p>
                <section aria-label="Deployment boundary" className="mt-7 rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4">
                  <div className="flex items-start gap-3">
                    <span className="grid size-9 shrink-0 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)]">
                      <Cloud className="size-4" />
                    </span>
                    <div>
                      <p className="text-sm font-semibold text-[var(--ls-text)]">
                        {deployment.mode === "cloud" ? "Open Review Cloud" : "Self-hosted deployment"}
                      </p>
                      <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">
                        Every workspace in this deployment uses this hosting boundary; it cannot be changed from a browser form.
                      </p>
                    </div>
                  </div>
                  <div className="mt-3 flex items-center gap-2 border-t border-[var(--ls-line)] pt-3 text-xs text-[var(--ls-text-secondary)]">
                    <MapPin className="size-3.5 text-[var(--ls-accent)]" />
                    <span className="font-medium text-[var(--ls-text)]">{deployment.region}</span>
                  </div>
                </section>
                <Link className="luminous-focus mt-7 inline-flex items-center gap-2 text-sm font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]" href="/#self-host"><CircleHelp className="size-4" />Read self-hosting boundary</Link>
              </div>
              <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-float)] sm:p-7">
                <h2 className="text-xl font-semibold tracking-[-0.04em] text-[var(--ls-text)]">Workspace details</h2>
                <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">Choose a stable organization name and URL. Availability is checked by the control plane when you continue.</p>
                {sessionState === "unavailable" ? (
                  <PageState
                    action={<RecoveryAction href="/workspaces/new">Retry access check</RecoveryAction>}
                    className="mt-8"
                    detail="The control plane could not verify your sign-in. Workspace creation is unavailable until access can be checked."
                    kind="unavailable"
                    title="Access check unavailable"
                  />
                ) : <CreateWorkspaceForm deployment={deployment} />}
              </section>
            </div>
          </section>
        </div>
      </main>
    </LuminousPublicFrame>
  );
}
