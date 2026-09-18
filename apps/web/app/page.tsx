import Link from "next/link";
import { ArrowRight, Command, ShieldCheck } from "lucide-react";

export default function WelcomePage() {
  return (
    <main className="grid min-h-screen place-items-center bg-console-canvas px-6 text-zinc-100">
      <section className="max-w-xl text-center">
        <span className="mx-auto mb-6 grid size-12 place-items-center rounded-2xl bg-gradient-to-br from-cyan-300 to-indigo-500 text-console-on-accent shadow-2xl shadow-cyan-500/15">
          <Command className="size-6" strokeWidth={2.5} />
        </span>
        <p className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-cyan-300">
          Open Review Console
        </p>
        <h1 className="text-4xl font-semibold tracking-[-0.05em] text-white">
          Operational clarity for evidence-first review.
        </h1>
        <p className="mt-5 text-base leading-7 text-zinc-400">
          The console reads review runs and governance policy from the control
          plane. Authentication and repository installation remain explicit
          connection steps.
        </p>
        <div className="mt-8 flex flex-col justify-center gap-3 sm:flex-row">
          <Link
            className="inline-flex h-10 items-center justify-center gap-2 rounded-xl bg-white px-4 text-sm font-medium text-zinc-950 transition hover:bg-zinc-200"
            href="/acme/home"
          >
            Open workspace <ArrowRight className="size-4" />
          </Link>
          <Link
            className="inline-flex h-10 items-center justify-center gap-2 rounded-xl border border-white/10 px-4 text-sm font-medium text-zinc-200 transition hover:bg-white/[0.05]"
            href="/acme/connect"
          >
            <ShieldCheck className="size-4 text-cyan-300" />
            Connection requirements
          </Link>
        </div>
      </section>
    </main>
  );
}
