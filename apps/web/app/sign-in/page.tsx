import Link from "next/link";
import {
  ArrowRight,
  Check,
  CircleAlert,
  KeyRound,
  LockKeyhole,
  Server,
  ShieldCheck,
  Sparkles,
} from "lucide-react";
import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { ProductMark } from "@/components/marketing/marketing-shell";
import {
  LuminousPublicFrame,
  PublicThemeToggle,
} from "@/components/onboarding/luminous-public-frame";
import { getOIDCConfiguration } from "@/lib/auth/oidc";
import { shouldRedirectExistingSession } from "@/lib/auth/safe-path";
import {
  getConsoleSession,
  localPreviewAvailable,
  safeInternalPath,
} from "@/lib/auth/session";

const errorMessages: Record<string, string> = {
  configuration:
    "Single sign-on is not configured for this deployment yet. Ask an operator to complete the Casdoor settings.",
  cancelled:
    "Sign-in was cancelled or access was declined by the identity provider. You can start a new attempt below.",
  exchange:
    "The identity provider did not accept the authorization-code exchange. Start again or verify the callback configuration.",
  origin:
    "This local address is not the configured sign-in callback. Open the configured Console URL, or use the development-only local-preview profile with an exact registered callback and matching OPEN_REVIEW_APP_URL.",
  provider:
    "The identity provider did not complete authorization. Start a new attempt below; if it keeps happening, ask an operator to check the provider's application access and callback settings.",
  state:
    "This sign-in attempt could not be verified. Restart sign-in in the same browser and allow cookies for this site; if the problem repeats, ask an operator to check the callback host.",
  session:
    "Your previous session is no longer accepted. Start a new organization sign-in to return to your workspace.",
  token:
    "The identity provider did not return an ID token that the control plane can verify.",
  unavailable:
    "The identity provider could not be reached. Check the issuer URL and try again.",
};

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string | string[]; next?: string | string[] }>;
}) {
  const query = await searchParams;
  const next = safeInternalPath(
    typeof query.next === "string" ? query.next : undefined,
  );
  const session = await getConsoleSession();
  const errorCode = typeof query.error === "string" ? query.error : undefined;
  if (session && shouldRedirectExistingSession(errorCode)) redirect(next);

  const error = errorCode ? errorMessages[errorCode] : undefined;
  const oidcConfigured = Boolean(getOIDCConfiguration());
  const localPreview = localPreviewAvailable();
  const destination = encodeURIComponent(next);
  const requestHeaders = await headers();
  const requestHost = requestHeaders.get("host")?.toLowerCase() ?? "";
  const requestProtocol = requestHeaders.get("x-forwarded-proto")?.split(",")[0]?.trim() === "https" ? "https:" : "http:";
  const configuredOrigin = process.env.OPEN_REVIEW_APP_URL?.trim();
  let localOriginMismatch = false;
  let configuredSignInHref: string | undefined;
  if (/^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/.test(requestHost) && configuredOrigin) {
    try {
      const configured = new URL(configuredOrigin);
      // This is a deployment-owned destination, never a request Host or a
      // query-provided redirect. Match the login route's plain-origin rule.
      const validOrigin = !configured.username && !configured.password &&
        !configured.search && !configured.hash && configured.pathname === "/" &&
        (configured.protocol === "https:" || (localPreview && configured.protocol === "http:"));
      if (validOrigin) {
        localOriginMismatch = configured.origin !== `${requestProtocol}//${requestHost}`;
        if (localOriginMismatch) {
          const signIn = new URL("/sign-in", configured.origin);
          signIn.searchParams.set("next", next);
          configuredSignInHref = signIn.toString();
        }
      }
    } catch {
      // The login route owns invalid-origin handling. Do not hide the SSO
      // configuration error behind a render failure on the sign-in page.
    }
  }
  const visibleError = error ?? (localOriginMismatch ? errorMessages.origin : undefined);

  return (
    <LuminousPublicFrame>
      <main className="min-h-screen p-3 sm:p-5">
        <div className="mx-auto grid min-h-[calc(100vh-1.5rem)] max-w-[1480px] overflow-hidden rounded-[28px] border border-[var(--ls-line)] bg-[var(--ls-surface-raised)] shadow-[var(--ls-shadow-float)] lg:min-h-[min(840px,calc(100vh-2.5rem))] lg:grid-cols-[minmax(0,.92fr)_minmax(520px,1.08fr)]">
          <section className="relative hidden overflow-hidden border-r border-[var(--ls-line)] bg-[linear-gradient(155deg,color-mix(in_srgb,var(--ls-accent-soft)_65%,var(--ls-surface))_0%,var(--ls-surface)_58%,color-mix(in_srgb,var(--ls-accent-soft)_38%,var(--ls-surface))_100%)] p-10 lg:flex lg:flex-col">
            <ProductMark variant="luminous" />
            <div className="my-auto max-w-md">
              <span className="grid size-12 place-items-center rounded-[16px] bg-[var(--ls-accent)] text-white shadow-[var(--ls-shadow-float)]">
                <Sparkles className="size-5" />
              </span>
              <h1 className="mt-8 text-[42px] font-semibold leading-[1.03] tracking-[-0.065em] text-[var(--ls-text)]">
                Better code through shared intelligence.
              </h1>
              <p className="mt-6 max-w-sm text-base leading-7 text-[var(--ls-text-secondary)]">
                Evidence-first review for teams that need every automated decision to remain explainable, governed, and recoverable.
              </p>
              <ul className="mt-9 space-y-4 text-sm text-[var(--ls-text-secondary)]">
                {[
                  "Keep code and credentials in the right boundary",
                  "Connect cloud or self-hosted Git providers",
                  "Keep rule, evidence, and approval history together",
                ].map((item) => (
                  <li className="flex items-center gap-3" key={item}>
                    <span className="grid size-5 place-items-center rounded-full bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
                      <Check className="size-3" />
                    </span>
                    {item}
                  </li>
                ))}
              </ul>
            </div>
            <div className="relative overflow-hidden rounded-[20px] border border-[color:color-mix(in_srgb,var(--ls-accent)_18%,var(--ls-line))] bg-[color:color-mix(in_srgb,var(--ls-surface)_74%,var(--ls-accent-soft))] p-5">
              <div className="absolute -right-8 -top-10 size-40 rounded-full bg-[var(--ls-accent-soft)] blur-3xl" />
              <div className="relative flex items-center gap-3">
                <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-surface)] text-[var(--ls-accent)] shadow-[var(--ls-shadow-control)]">
                  <ShieldCheck className="size-5" />
                </span>
                <div>
                  <p className="text-sm font-semibold text-[var(--ls-text)]">Identity before integration</p>
                  <p className="mt-1 text-xs leading-5 text-[var(--ls-text-secondary)]">Create a secure organization context first; connect Git after access is verified.</p>
                </div>
              </div>
            </div>
          </section>

          <section className="flex min-h-full flex-col p-5 sm:p-8 lg:p-10">
            <header className="flex items-center justify-between gap-4">
              <ProductMark variant="luminous" />
              <div className="flex items-center gap-1">
                <Link className="luminous-focus hidden rounded-[10px] px-3 py-2 text-sm text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface-muted)] hover:text-[var(--ls-text)] sm:inline-flex" href="/">
                  Back to site
                </Link>
                <PublicThemeToggle />
              </div>
            </header>

            <div className="mx-auto flex w-full max-w-[430px] flex-1 flex-col justify-center py-12">
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--ls-accent)]">Workspace access</p>
              <h2 className="mt-3 text-3xl font-semibold tracking-[-0.055em] text-[var(--ls-text)]">Sign in to Open Review</h2>
              <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">Your organization identity opens the workspaces and review evidence you are allowed to access.</p>

              {visibleError ? (
                <div className="mt-6 flex gap-3 rounded-[14px] border border-[color:color-mix(in_srgb,var(--ls-warning)_30%,transparent)] bg-[color:color-mix(in_srgb,var(--ls-warning)_10%,var(--ls-surface))] p-3.5 text-sm leading-5 text-[var(--ls-warning-text)]">
                  <CircleAlert className="mt-0.5 size-4 shrink-0" />
                  {visibleError}
                </div>
              ) : null}

              <div className="mt-7 rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)]">
                <div className="flex items-start gap-3">
                  <span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><KeyRound className="size-4.5" /></span>
                  <div>
                    <p className="text-sm font-semibold text-[var(--ls-text)]">Organization single sign-on</p>
                    <p className="mt-1 text-xs leading-5 text-[var(--ls-text-tertiary)]">Authorization Code + PKCE. The browser never receives a provider credential.</p>
                  </div>
                </div>
                {oidcConfigured && !localOriginMismatch ? (
                  <a className="luminous-focus mt-5 inline-flex h-11 w-full items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)]" href={`/api/auth/login?next=${destination}`}>
                    Continue with organization SSO <ArrowRight className="size-4" />
                  </a>
                ) : localOriginMismatch ? (
                  <div className="mt-5 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
                    <p>Sign-in is unavailable from this local address until its exact callback origin is registered and configured for this deployment.</p>
                    {configuredSignInHref ? (
                      <a className="luminous-focus mt-3 inline-flex min-h-10 items-center gap-2 rounded-[9px] bg-[var(--ls-accent)] px-3 text-xs font-semibold text-white hover:bg-[var(--ls-accent-hover)]" href={configuredSignInHref}>
                        Open configured Console <ArrowRight className="size-3.5" />
                      </a>
                    ) : null}
                  </div>
                ) : (
                  <div className="mt-5 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3 text-xs leading-5 text-[var(--ls-text-secondary)]">
                    Set <code className="rounded bg-[var(--ls-surface)] px-1 py-0.5 text-[var(--ls-text)]">CASDOOR_ISSUER</code> and <code className="rounded bg-[var(--ls-surface)] px-1 py-0.5 text-[var(--ls-text)]">CASDOOR_CLIENT_ID</code> to enable this deployment’s identity provider.
                  </div>
                )}
                {localPreview ? (
                  <a className="luminous-focus mt-3 inline-flex h-10 w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-4 text-sm font-medium text-[var(--ls-text)] transition hover:bg-[var(--ls-surface-muted)]" href={`/api/auth/local?next=${destination}`}>
                    Open local development preview <ArrowRight className="size-4" />
                  </a>
                ) : null}
              </div>

              <div className="mt-5 grid gap-2 sm:grid-cols-2">
                <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3.5 py-3">
                  <Server className="size-4 text-[var(--ls-accent)]" />
                  <p className="mt-2 text-xs font-medium text-[var(--ls-text)]">Self-hosted ready</p>
                  <p className="mt-1 text-[11px] leading-4 text-[var(--ls-text-tertiary)]">Use the deployment-owned identity provider and Git endpoint.</p>
                </div>
                <div className="rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3.5 py-3">
                  <LockKeyhole className="size-4 text-[var(--ls-accent)]" />
                  <p className="mt-2 text-xs font-medium text-[var(--ls-text)]">No provider secret</p>
                  <p className="mt-1 text-[11px] leading-4 text-[var(--ls-text-tertiary)]">GitHub and GitLab credentials are attached after workspace access.</p>
                </div>
              </div>

              <p className="mt-6 text-center text-xs leading-5 text-[var(--ls-text-tertiary)]">
                Need a private GitLab path? <Link className="font-medium text-[var(--ls-accent)] hover:text-[var(--ls-accent-hover)]" href="/#self-host">Read the self-hosting boundary</Link>.
              </p>
            </div>
            <p className="flex items-center gap-2 text-xs text-[var(--ls-text-tertiary)]"><LockKeyhole className="size-3.5" />The control plane validates the OIDC ID token for every management request.</p>
          </section>
        </div>
      </main>
    </LuminousPublicFrame>
  );
}
