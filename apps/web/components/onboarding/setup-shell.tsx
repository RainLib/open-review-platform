import Link from "next/link";
import type { ReactNode } from "react";
import { Check, LockKeyhole } from "lucide-react";

import { ProductMark } from "@/components/marketing/marketing-shell";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { ProviderMark } from "@/components/providers/provider-icons";
import { cn } from "@/lib/utils";

const steps = ["Sign in", "Connect Git", "Open workspace"];

export function SetupShell({
  activeStep,
  children,
}: {
  activeStep: 1 | 2 | 3;
  children: ReactNode;
}) {
  return (
    <LuminousPublicFrame>
      <div className="min-h-screen overflow-hidden">
        <header className="luminous-frosted relative z-10 mx-auto flex max-w-6xl items-center justify-between border-b border-[var(--ls-line)] px-5 py-4 sm:px-8">
          <ProductMark variant="luminous" />
          <div className="flex items-center gap-2">
            <Link className="luminous-focus rounded-lg px-2 py-1.5 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)]" href="/">
              Back to site
            </Link>
            <PublicThemeToggle />
          </div>
        </header>
        <div className="relative mx-auto max-w-6xl px-5 pb-16 pt-10 sm:px-8 lg:pt-16">
        <ol className="mx-auto mb-10 flex max-w-xl items-center justify-between gap-2 sm:mb-14" aria-label="Setup progress">
          {steps.map((step, index) => {
            const number = index + 1;
            const complete = number < activeStep;
            const active = number === activeStep;
            return (
              <li className="flex min-w-0 flex-1 items-center gap-2" key={step}>
                <span
                  className={cn(
                    "grid size-7 shrink-0 place-items-center rounded-full border text-xs font-semibold",
                    complete
                      ? "border-[var(--ls-accent)] bg-[var(--ls-accent)] text-white"
                      : active
                        ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"
                        : "border-[var(--ls-line)] bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)]",
                  )}
                >
                  {complete ? <Check className="size-3.5" /> : number}
                </span>
                <span
                  className={cn(
                    "hidden text-xs font-medium sm:inline",
                    active || complete
                      ? "text-[var(--ls-text)]"
                      : "text-[var(--ls-text-tertiary)]",
                  )}
                >
                  {step}
                </span>
                {number < steps.length ? (
                  <span className="h-px flex-1 bg-[var(--ls-line)]" />
                ) : null}
              </li>
            );
          })}
        </ol>
        {children}
        </div>
        <div className="relative mx-auto flex max-w-6xl items-center justify-center gap-2 px-5 pb-8 text-xs text-[var(--ls-text-tertiary)]">
          <LockKeyhole className="size-3.5" />
          Provider secrets never enter the browser.
        </div>
      </div>
    </LuminousPublicFrame>
  );
}

export function SetupHeading({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string;
  title: string;
  description: string;
}) {
  return (
    <div className="mx-auto max-w-2xl text-center">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">
        {eyebrow}
      </p>
      <h1 className="mt-3 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)] sm:text-5xl">
        {title}
      </h1>
      <p className="mx-auto mt-4 max-w-xl text-sm leading-6 text-[var(--ls-text-secondary)] sm:text-base">
        {description}
      </p>
    </div>
  );
}

export function SetupCard({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <section className={cn("rounded-3xl border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] p-5 shadow-[var(--ls-shadow-float)] backdrop-blur-sm sm:p-7", className)}>
      {children}
    </section>
  );
}

export function ProviderGlyph({ label }: { label: "GitHub" | "GitLab" }) {
  return (
    <span className={cn("grid size-11 place-items-center rounded-2xl border border-[var(--ls-line)] bg-[var(--ls-surface)]", label === "GitHub" ? "text-[var(--ls-text)]" : "text-[#fc6d26]")}>
      <ProviderMark className="size-6" provider={label === "GitHub" ? "github" : "gitlab"} />
    </span>
  );
}
