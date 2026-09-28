import Link from "next/link";
import {
  ArrowRight,
  Check,
  ChevronRight,
  CircleCheckBig,
  FileSearch,
  GitBranch,
  GitPullRequest,
  Layers3,
  LockKeyhole,
  MessageSquareText,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { ProviderMark } from "@/components/providers/provider-icons";

const reviewSignals = [
  { icon: ShieldCheck, label: "Security", detail: "2 findings · 1 suggestion", tone: "success" },
  { icon: CircleCheckBig, label: "Correctness", detail: "Looks good", tone: "success" },
  { icon: Sparkles, label: "Performance", detail: "1 suggestion", tone: "warning" },
  { icon: FileSearch, label: "Maintainability", detail: "Looks good", tone: "success" },
] as const;

const reviewChain = [
  {
    icon: GitPullRequest,
    title: "Receive the change",
    detail: "A signed provider event becomes one durable review request.",
  },
  {
    icon: Layers3,
    title: "Resolve the policy",
    detail: "Each run pins the exact rule and configuration revisions it used.",
  },
  {
    icon: MessageSquareText,
    title: "Publish evidence",
    detail: "Comments, checks, prompts, and delivery receipts stay connected to the revision.",
  },
];

const selfHostPromises = [
  "Your Git endpoint and credentials remain deployment-owned.",
  "Workers use isolated, ephemeral checkouts for each review.",
  "Policies, evidence, and audit records stay workspace-scoped.",
];

export default function LandingPage() {
  return (
    <LuminousPublicFrame>
      <main className="min-h-screen overflow-hidden bg-[var(--ls-canvas)] text-[var(--ls-text)] selection:bg-[var(--ls-accent)] selection:text-white">
        <div className="luminous-public-hero-glow pointer-events-none fixed inset-x-0 top-0 h-[640px]" />
        <PublicHeader />

        <section className="relative mx-auto max-w-[1480px] px-5 pb-20 pt-14 sm:px-8 sm:pb-28 sm:pt-20 lg:px-10 lg:pb-32">
          <div className="grid grid-cols-[minmax(0,1fr)] gap-12 lg:grid-cols-[minmax(0,.88fr)_minmax(510px,1.12fr)] lg:items-center lg:gap-16">
            <div className="min-w-0 max-w-[610px]">
              <span className="inline-flex items-center gap-2 rounded-full border border-[color:color-mix(in_srgb,var(--ls-accent)_19%,var(--ls-line))] bg-[var(--ls-accent-soft)] px-3 py-1.5 text-xs font-semibold text-[var(--ls-accent)]">
                <Sparkles className="size-3.5" /> Evidence-first AI code review
              </span>
              <h1 className="mt-6 max-w-[590px] text-5xl font-semibold leading-[.98] tracking-[-0.073em] text-[var(--ls-text)] sm:text-6xl lg:text-7xl">
                Review evidence <span className="text-[var(--ls-accent)]">you can trust.</span>
              </h1>
              <p className="mt-6 max-w-[535px] text-base leading-7 text-[var(--ls-text-secondary)] sm:text-lg">
                Open Review turns every pull request into a clear, governed decision — with source links, policy snapshots, and durable delivery evidence.
              </p>
              <div className="mt-8 flex flex-col gap-3 sm:flex-row">
                <Link className="luminous-focus inline-flex h-11 items-center justify-center gap-2 rounded-[11px] bg-[var(--ls-accent)] px-5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:-translate-y-0.5 hover:bg-[var(--ls-accent-hover)]" href="/sign-in?next=%2Fworkspaces">
                  Get started <ArrowRight className="size-4" />
                </Link>
                <a className="luminous-focus inline-flex h-11 items-center justify-center gap-2 rounded-[11px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-raised)] px-5 text-sm font-medium text-[var(--ls-text)] shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-surface-muted)]" href="#how-it-works">
                  How it works <ChevronRight className="size-4" />
                </a>
              </div>
              <div className="mt-8 flex flex-wrap gap-x-5 gap-y-2 text-xs text-[var(--ls-text-secondary)]">
                {["GitHub & GitLab", "Self-host or cloud", "Enterprise-ready"].map((item) => (
                  <span className="inline-flex items-center gap-1.5" key={item}><Check className="size-3.5 text-[var(--ls-success-text)]" />{item}</span>
                ))}
              </div>
            </div>
            <ReviewPreview />
          </div>
        </section>

        <section className="relative border-y border-[var(--ls-line)] bg-[var(--ls-surface-raised)]" id="how-it-works">
          <div className="mx-auto max-w-[1480px] px-5 py-16 sm:px-8 sm:py-20 lg:px-10">
            <div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
              <div className="max-w-2xl"><p className="text-xs font-semibold uppercase tracking-[.17em] text-[var(--ls-accent)]">Designed for accountable automation</p><h2 className="mt-3 text-3xl font-semibold tracking-[-.055em] text-[var(--ls-text)] sm:text-4xl">Useful review is a chain of evidence, not a chat transcript.</h2></div>
              <p className="max-w-md text-sm leading-6 text-[var(--ls-text-secondary)]">The engine stays focused on the diff; the control plane preserves identity, queueing, policy, delivery, and audit around every result.</p>
            </div>
            <ol className="mt-10 grid gap-4 lg:grid-cols-3">
              {reviewChain.map((item, index) => {
                const Icon = item.icon;
                return <li className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]" key={item.title}><span className="flex items-center justify-between text-xs font-mono text-[var(--ls-text-tertiary)]"><span>0{index + 1}</span><Icon className="size-4 text-[var(--ls-accent)]" /></span><h3 className="mt-8 text-lg font-semibold tracking-[-.035em] text-[var(--ls-text)]">{item.title}</h3><p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">{item.detail}</p></li>;
              })}
            </ol>
          </div>
        </section>

        <section className="relative mx-auto grid max-w-[1480px] gap-10 px-5 py-20 sm:px-8 sm:py-24 lg:grid-cols-[minmax(0,1fr)_430px] lg:items-center lg:px-10" id="self-host">
          <div><p className="text-xs font-semibold uppercase tracking-[.17em] text-[var(--ls-accent)]">Private Git is a first-class path</p><h2 className="mt-3 max-w-2xl text-3xl font-semibold tracking-[-.055em] text-[var(--ls-text)] sm:text-4xl">Run review beside the systems that already hold your source.</h2><p className="mt-5 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">For GitLab self-managed, the endpoint and service credential come from a deployment profile. People sign in to Open Review first, then connect their approved private instance without sending tokens through the browser.</p><Link className="luminous-focus mt-7 inline-flex items-center gap-2 text-sm font-semibold text-[var(--ls-accent)] transition hover:text-[var(--ls-accent-hover)]" href="/sign-in?next=%2Fworkspaces">Open a workspace <ArrowRight className="size-4" /></Link></div>
          <div className="rounded-[22px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] p-6 shadow-[var(--ls-shadow-float)]"><div className="flex items-center gap-3"><span className="grid size-11 place-items-center rounded-[14px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><LockKeyhole className="size-5" /></span><div><p className="font-semibold text-[var(--ls-text)]">Self-hosting boundary</p><p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">Same review surface, deployment-owned trust.</p></div></div><ul className="mt-6 space-y-4">{selfHostPromises.map((promise) => <li className="flex gap-3 text-sm leading-6 text-[var(--ls-text-secondary)]" key={promise}><Check className="mt-1 size-3.5 shrink-0 text-[var(--ls-success-text)]" />{promise}</li>)}</ul></div>
        </section>

        <footer className="relative border-t border-[var(--ls-line)]"><div className="mx-auto flex max-w-[1480px] flex-col gap-4 px-5 py-7 text-xs text-[var(--ls-text-tertiary)] sm:flex-row sm:items-center sm:justify-between sm:px-8 lg:px-10"><span>Open Review Platform · evidence-first code review</span><div className="flex gap-4"><Link className="hover:text-[var(--ls-text)]" href="/sign-in">Sign in</Link><Link className="hover:text-[var(--ls-text)]" href="/workspaces">Workspaces</Link></div></div></footer>
      </main>
    </LuminousPublicFrame>
  );
}

function PublicHeader() {
  return <header className="luminous-frosted relative z-10 border-b border-[var(--ls-line)]"><div className="mx-auto flex h-16 max-w-[1480px] items-center justify-between px-5 max-[359px]:px-3 max-[359px]:whitespace-nowrap sm:px-8 lg:px-10"><ProductMark variant="luminous" /><nav aria-label="Primary navigation" className="hidden items-center gap-6 text-sm text-[var(--ls-text-secondary)] md:flex"><a className="transition hover:text-[var(--ls-text)]" href="#how-it-works">Product</a><a className="transition hover:text-[var(--ls-text)]" href="#self-host">Self-hosting</a><Link className="transition hover:text-[var(--ls-text)]" href="/workspaces">Workspaces</Link></nav><div className="flex items-center gap-1.5"><Link className="luminous-focus hidden rounded-[10px] px-3 py-2 text-sm font-medium text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)] sm:inline-flex" href="/sign-in">Sign in</Link><PublicThemeToggle /><Link className="luminous-focus inline-flex h-9 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-3.5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href="/sign-in?next=%2Fworkspaces">Get started <ArrowRight className="size-3.5" /></Link></div></div></header>;
}

function ReviewPreview() {
  return <div className="relative mx-auto w-full max-w-[680px]"><div className="absolute -inset-8 -z-10 rounded-full bg-[var(--ls-accent-soft)] opacity-70 blur-3xl" /><section aria-label="Example pull request evidence" className="overflow-hidden rounded-[24px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-raised)] p-3 shadow-[var(--ls-shadow-float)] backdrop-blur-xl sm:p-4"><div className="overflow-hidden rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)]"><div className="flex items-center justify-between border-b border-[var(--ls-line)] px-4 py-3.5 sm:px-5"><div className="flex min-w-0 items-center gap-3"><span className="grid size-9 place-items-center rounded-[11px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><GitPullRequest className="size-4" /></span><div className="min-w-0"><p className="truncate text-sm font-semibold text-[var(--ls-text)]">Pull request <span className="font-mono text-[var(--ls-text-secondary)]">#4827</span></p><p className="mt-0.5 truncate text-xs text-[var(--ls-text-tertiary)]">Risk-based review · policy pinned</p></div></div><span className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-[11px] font-semibold text-[var(--ls-success-text)]"><Check className="size-3" /> Reviewed</span></div><div className="grid gap-2.5 p-4 sm:p-5">{reviewSignals.map((signal) => { const Icon = signal.icon; const success = signal.tone === "success"; return <div className="flex items-center gap-3 rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3.5 py-3" key={signal.label}><span className="grid size-8 place-items-center rounded-[10px] bg-[var(--ls-surface)] text-[var(--ls-accent)] shadow-[var(--ls-shadow-control)]"><Icon className="size-4" /></span><div className="min-w-0 flex-1"><p className="text-sm font-semibold text-[var(--ls-text)]">{signal.label}</p><p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">{signal.detail}</p></div><span className={success ? "text-[var(--ls-success-text)]" : "text-[var(--ls-warning-text)]"}>{success ? <CircleCheckBig className="size-4" /> : <CircleAlertDot />}</span></div>; })}</div><div className="border-t border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4 sm:p-5"><div className="flex items-start gap-3"><span className="grid size-9 shrink-0 place-items-center rounded-[11px] bg-[var(--ls-surface)] text-[var(--ls-accent)] shadow-[var(--ls-shadow-control)]"><GitBranch className="size-4" /></span><div><p className="text-sm font-semibold text-[var(--ls-text)]">Evidence-linked review</p><p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Every conclusion points back to an exact revision, source location, applied policy, and delivery receipt.</p></div></div></div></div><div className="grid grid-cols-2 gap-px border-x border-b border-[var(--ls-line)] bg-[var(--ls-line)] sm:grid-cols-4"><ProviderCell label="GitHub" provider="github" /><ProviderCell label="GitLab" provider="gitlab" /><ProviderCell icon={<ShieldCheck className="size-4" />} label="Policy" /><ProviderCell icon={<LockKeyhole className="size-4" />} label="Audit" /></div></section></div>;
}

function CircleAlertDot() { return <span aria-label="Needs attention" className="grid size-4 place-items-center rounded-full border-2 border-current"><span className="size-1 rounded-full bg-current" /></span>; }

function ProviderCell({ icon, label, provider }: { icon?: React.ReactNode; label: string; provider?: "github" | "gitlab" }) { return <div className="flex items-center justify-center gap-2 bg-[var(--ls-surface)] px-2 py-3 text-xs font-medium text-[var(--ls-text-secondary)]">{provider ? <ProviderMark className="size-4 text-[var(--ls-text)]" provider={provider} /> : <span className="text-[var(--ls-accent)]">{icon}</span>}{label}</div>; }
