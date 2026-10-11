"use client";

import { FormEvent, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  BadgeCheck,
  Check,
  CheckCircle2,
  CircleAlert,
  CircleDashed,
  Clipboard,
  CloudCog,
  Fingerprint,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  Network,
  Plus,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Trash2,
  UserRoundCog,
} from "lucide-react";

import { EnterpriseSettingsTabs } from "@/components/console/enterprise-settings-tabs";
import { DataFreshness, PageState } from "@/components/console/page-state";
import { TabStateRouter } from "@/components/console/tab-state-router";
import { useModalFocus } from "@/components/console/use-modal-focus";
import type {
  SSOConfiguration,
  SSOData,
  SSODomain,
  SSOProbeReceipt,
  SSORoleMapping,
} from "@/lib/control-api";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

type SSOTab = "overview" | "identity-provider" | "domains-mapping";
type PendingAction =
  | "save"
  | "probe"
  | "domain"
  | "mapping"
  | "enforce"
  | "suspend"
  | `verify:${string}`
  | `delete:${string}`;

const readinessLabels: Record<string, string> = {
  identity_provider_not_configured: "Save an identity-provider draft.",
  current_revision_not_tested: "Test the current configuration revision.",
  verified_domain_required: "Verify at least one workspace email domain.",
  role_mapping_required: "Add at least one group-to-role mapping.",
};

const stateLabels: Record<SSOConfiguration["state"], string> = {
  draft_saved: "Draft saved",
  testing: "Connection testing",
  verified: "Connection verified",
  enforcement_ready: "Ready to enforce",
  enforced: "SSO policy enforced",
  suspended: "Enforcement suspended",
};

export function SSOManager({ data, org, tab }: { data: SSOData; org: string; tab: SSOTab }) {
  const router = useRouter();
  const enabled = data.source === "live";
  const [pending, setPending] = useState<PendingAction>();
  const [notice, setNotice] = useState<{ tone: "success" | "error"; text: string }>();
  const [confirmEnforce, setConfirmEnforce] = useState(false);

  async function mutate(path: string, action: PendingAction, init: RequestInit, success: string) {
    setPending(action);
    setNotice(undefined);
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/sso${path}`, init);
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      if (!response.ok) throw new Error(body.error ?? `Request failed (${response.status}).`);
      setNotice({ tone: "success", text: success });
      router.refresh();
      return true;
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : "The SSO request could not be completed." });
      return false;
    } finally {
      setPending(undefined);
    }
  }

  async function saveConfiguration(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const protocol = String(form.get("protocol")) as "oidc" | "saml";
    const body = {
      protocol,
      display_name: String(form.get("display_name") ?? "").trim(),
      issuer_url: protocol === "oidc" ? String(form.get("issuer_url") ?? "").trim() : "",
      metadata_url: protocol === "saml" ? String(form.get("metadata_url") ?? "").trim() : "",
      client_id: protocol === "oidc" ? String(form.get("client_id") ?? "").trim() : "",
      secret_ref: String(form.get("secret_ref") ?? "").trim(),
      keep_secret: form.get("keep_secret") === "on",
      group_claim: String(form.get("group_claim") ?? "groups").trim(),
      expected_revision: data.configuration?.revision ?? 0,
    };
    await mutate("/configuration", "save", jsonRequest("PUT", body), "Draft saved. Test this exact revision before relying on it.");
  }

  async function requestProbe() {
    if (!data.configuration) return;
    await mutate("/probes", "probe", jsonRequest("POST", { expected_revision: data.configuration.revision }), "Connection test queued. Refresh status when you want the latest durable receipt.");
  }

  async function addDomain(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    if (await mutate("/domains", "domain", jsonRequest("POST", { domain: String(form.get("domain") ?? "").trim() }), "Domain challenge created. Publish the TXT value, then verify it.")) {
      formElement.reset();
    }
  }

  async function addMapping(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    if (await mutate("/mappings", "mapping", jsonRequest("POST", {
      group_value: String(form.get("group_value") ?? "").trim(),
      role: String(form.get("role") ?? "viewer"),
      repository_scope: String(form.get("repository_scope") ?? "*").trim(),
    }), "Role mapping added. Owner access remains explicit and cannot be granted by a group mapping.")) {
      formElement.reset();
    }
  }

  async function enforce(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!data.configuration) return;
    const form = new FormData(event.currentTarget);
    const ok = await mutate("/enforce", "enforce", jsonRequest("POST", {
      expected_revision: data.configuration.revision,
      break_glass_subject: String(form.get("break_glass_subject") ?? "").trim(),
    }), "Workspace SSO policy is enforced. The named owner remains the audited break-glass path.");
    if (ok) setConfirmEnforce(false);
  }

  const state = data.configuration?.state;
  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--ls-accent)]">Enterprise identity</p>
          <div className="mt-2 flex min-w-0 items-center gap-2"><h1 className="text-[32px] font-semibold tracking-[-0.045em] text-[var(--ls-text)]">Single sign-on</h1><HelpHint label="Single sign-on">Configure the workspace policy mirror used with Casdoor federation. Saving, testing, domain ownership, role mapping, and enforcement are separate audited states.</HelpHint></div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <DataFreshness detail={data.detail} state={data.source} />
          {state ? <StatePill state={state} /> : null}
        </div>
      </header>

      <EnterpriseSettingsTabs active="sso" org={org} />
      <TabStateRouter className="flex gap-1 overflow-x-auto border-b border-[var(--ls-line)]" label="SSO settings views">
        <Subtab href={`/${org}/settings/sso?tab=overview`} label="Overview" selected={tab === "overview"} />
        <Subtab href={`/${org}/settings/sso?tab=identity-provider`} label="Identity provider" selected={tab === "identity-provider"} />
        <Subtab href={`/${org}/settings/sso?tab=domains-mapping`} label="Domains & role mapping" selected={tab === "domains-mapping"} />
      </TabStateRouter>

      {notice ? <Notice {...notice} /> : null}
      {data.source !== "live" && data.source !== "demo" ? <Unavailable detail={data.detail} /> : <>
        {data.source === "demo" ? <div className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">Preview identity data is read-only. It illustrates lifecycle evidence, but cannot change configuration, run probes, verify domains, alter mappings, suspend, or enforce SSO.</div> : null}
        <fieldset className="contents" disabled={!enabled}>
          {tab === "identity-provider" ? (
            <IdentityProvider configuration={data.configuration} latestProbe={data.probes[0]} onProbe={requestProbe} onSave={saveConfiguration} pending={pending} />
          ) : tab === "domains-mapping" ? (
            <DomainsAndMappings data={data} onAddDomain={addDomain} onAddMapping={addMapping} onDelete={(mapping) => mutate(`/mappings/${encodeURIComponent(mapping.id)}?revision=${mapping.revision}`, `delete:${mapping.id}`, { method: "DELETE" }, "Role mapping deleted.")} onVerify={(domain) => mutate(`/domains/${encodeURIComponent(domain.id)}/verify`, `verify:${domain.id}`, jsonRequest("POST", {}), "DNS ownership verified.")} pending={pending} />
          ) : (
            <Overview data={data} onOpenEnforce={() => setConfirmEnforce(true)} onProbe={requestProbe} onRefresh={() => router.refresh()} onSuspend={() => data.configuration && mutate("/suspend", "suspend", jsonRequest("POST", { expected_revision: data.configuration.revision }), "SSO enforcement suspended. The saved configuration remains available for repair and retest.")} pending={pending} />
          )}
        </fieldset>
      </>}

      {confirmEnforce && data.configuration ? <EnforcementSheet configuration={data.configuration} onClose={() => setConfirmEnforce(false)} onSubmit={enforce} pending={pending === "enforce"} /> : null}
    </div>
  );
}

function Overview({ data, onOpenEnforce, onProbe, onRefresh, onSuspend, pending }: { data: SSOData; onOpenEnforce: () => void; onProbe: () => void; onRefresh: () => void; onSuspend: () => void; pending?: PendingAction }) {
  const configuration = data.configuration;
  const latestProbe = data.probes[0];
  const activeProbe =
    latestProbe?.state === "queued" || latestProbe?.state === "running";
  const tested = Boolean(configuration?.tested_revision && configuration.tested_revision === configuration.revision);
  const verifiedDomains = data.domains.filter((item) => item.state === "verified").length;
  const steps = [
    { label: "Provider draft", detail: configuration ? `Revision ${configuration.revision}` : "Not configured", done: Boolean(configuration) },
    { label: "Metadata probe", detail: tested ? "Current revision verified" : latestProbe ? `${latestProbe.state} · revision ${latestProbe.config_revision}` : "Not tested", done: tested },
    { label: "Domain ownership", detail: `${verifiedDomains} verified`, done: verifiedDomains > 0 },
    { label: "Role mapping", detail: `${data.mappings.length} mapping${data.mappings.length === 1 ? "" : "s"}`, done: data.mappings.length > 0 },
  ];
  const readinessState = configuration?.state;
  const readinessPositive = data.readiness.ready && readinessState !== "suspended";
  const readinessTitle = readinessState === "enforced"
    ? "Enforcement active"
    : readinessState === "suspended"
      ? "Enforcement suspended"
      : "Enforcement readiness";
  const readinessMessage = readinessState === "enforced"
    ? `Revision ${configuration?.revision ?? "—"} is enforced. The retained break-glass owner remains the audited recovery path.`
    : readinessState === "suspended"
      ? "The saved provider evidence remains available, but the workspace is no longer enforcing SSO. Save a new revision, test it, and explicitly enforce again after recovery."
      : "All preconditions are satisfied. An owner must still review the break-glass path and explicitly enforce the policy.";
  return <div className="grid grid-cols-[minmax(0,1fr)] gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(320px,.75fr)]">
    <div className="min-w-0 space-y-5">
      <section className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between"><div><p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-text-tertiary)]">Deployment state</p><h2 className="mt-2 text-xl font-semibold text-[var(--ls-text)]">{configuration ? stateLabels[configuration.state] : "SSO is not configured"}</h2><p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">{configuration ? `${configuration.display_name} · ${configuration.protocol.toUpperCase()} · revision ${configuration.revision}` : "Start with an identity-provider draft. No login path changes until the current revision is tested and an owner explicitly enforces it."}</p></div>{configuration ? <StatePill state={configuration.state} /> : null}</div>
        <div className="mt-7 grid gap-3 md:grid-cols-2">{steps.map((step, index) => <div className="rounded-[15px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4" key={step.label}><div className="flex items-start gap-3"><span className={cn("grid size-8 shrink-0 place-items-center rounded-full text-xs font-semibold", step.done ? "bg-emerald-500/12 text-[var(--ls-success-text)]" : "bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)]")}>{step.done ? <Check className="size-4" /> : index + 1}</span><div><p className="text-sm font-semibold text-[var(--ls-text)]">{step.label}</p><p className="mt-1 text-xs text-[var(--ls-text-secondary)]">{step.detail}</p></div></div></div>)}</div>
        <div className="mt-6 flex flex-wrap gap-2">{configuration && !tested ? <button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45" disabled={pending === "probe" || configuration.state === "testing" || configuration.state === "enforced"} onClick={onProbe} type="button">{pending === "probe" || configuration.state === "testing" ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}Test connection</button> : null}{activeProbe ? <button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] px-4 text-sm font-medium text-[var(--ls-text)]" onClick={onRefresh} type="button"><RefreshCw className="size-4" />Refresh status</button> : null}{configuration?.state === "enforcement_ready" ? <button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white" onClick={onOpenEnforce} type="button"><LockKeyhole className="size-4" />Review enforcement</button> : null}{configuration?.state === "enforced" ? <button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-red-500/25 bg-red-500/[0.06] px-4 text-sm font-semibold text-[var(--ls-critical-text)] disabled:opacity-45" disabled={pending === "suspend"} onClick={onSuspend} type="button">{pending === "suspend" ? <LoaderCircle className="size-4 animate-spin" /> : <ShieldAlert className="size-4" />}Suspend enforcement</button> : null}<Link className={cn("luminous-focus inline-flex h-10 items-center rounded-[10px] px-4 text-sm font-medium", configuration ? "border border-[var(--ls-line-strong)] text-[var(--ls-text)]" : "bg-[var(--ls-accent)] font-semibold text-white")} href="?tab=identity-provider">{configuration ? "Edit provider" : "Configure provider"}</Link></div>
      </section>
      <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><div className="flex items-start gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><CloudCog className="size-5" /></span><div className="min-w-0 flex-1"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Latest durable connection test</h3>{latestProbe ? <ProbeSummary probe={latestProbe} /> : <p className="mt-2 text-sm text-[var(--ls-text-secondary)]">No metadata probe has been requested.</p>}</div></div></section>
    </div>
    <aside className="min-w-0 space-y-5">
      <section className={cn("rounded-[18px] border p-5", readinessPositive ? "border-emerald-500/20 bg-emerald-500/[0.05]" : "border-amber-500/20 bg-amber-500/[0.05]")}><div className="flex items-center gap-2"><ShieldCheck className={cn("size-4", readinessPositive ? "text-[var(--ls-success-text)]" : "text-[var(--ls-warning-text)]")} /><h3 className="text-sm font-semibold text-[var(--ls-text)]">{readinessTitle}</h3></div>{data.readiness.ready ? <p className="mt-3 text-sm leading-6 text-[var(--ls-text-secondary)]">{readinessMessage}</p> : <ul className="mt-3 space-y-2">{data.readiness.blockers.map((blocker) => <li className="flex gap-2 text-xs leading-5 text-[var(--ls-text-secondary)]" key={blocker}><CircleDashed className="mt-0.5 size-3.5 shrink-0 text-[var(--ls-warning-text)]" />{readinessLabels[blocker] ?? blocker.replaceAll("_", " ")}</li>)}</ul>}</section>
      <Boundary icon={Fingerprint} title="Casdoor federation boundary">This workspace record is the policy mirror. Casdoor remains the browser identity broker; the control plane verifies its signed token and then applies tenant membership and role policy.</Boundary>
      <Boundary icon={LockKeyhole} title="Break-glass invariant">Group mappings never grant owner. Enforcement requires an existing explicit owner subject that remains available for audited recovery.</Boundary>
    </aside>
  </div>;
}

function IdentityProvider({ configuration, latestProbe, onProbe, onSave, pending }: { configuration?: SSOConfiguration; latestProbe?: SSOProbeReceipt; onProbe: () => void; onSave: (event: FormEvent<HTMLFormElement>) => void; pending?: PendingAction }) {
  const [protocol, setProtocol] = useState<"oidc" | "saml">(configuration?.protocol ?? "oidc");
  const locked = configuration?.state === "enforced";
  const currentTested = Boolean(configuration?.tested_revision && configuration.tested_revision === configuration.revision);
  return <div className="grid grid-cols-[minmax(0,1fr)] gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,.8fr)]"><form className="min-w-0 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-7" onSubmit={onSave}><div className="flex items-start justify-between gap-4"><div><p className="text-xs font-semibold uppercase tracking-[0.1em] text-[var(--ls-accent)]">Configuration draft</p><div className="mt-2 flex min-w-0 items-center gap-2"><h2 className="text-xl font-semibold text-[var(--ls-text)]">Identity provider</h2><HelpHint label="Identity provider">A save creates a new revision and invalidates earlier test evidence. Secret references are resolved server-side and never returned.</HelpHint></div></div>{configuration ? <span className="shrink-0 rounded-full bg-[var(--ls-surface-muted)] px-3 py-1.5 font-mono text-xs text-[var(--ls-text-secondary)]">r{configuration.revision}</span> : null}</div>
    {locked ? <div className="mt-5 rounded-[12px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-sm text-[var(--ls-warning-text)]">Suspend enforcement from Overview before changing this provider.</div> : null}
    <fieldset className="mt-6" disabled={locked}><legend className="text-xs font-medium text-[var(--ls-text-secondary)]">Protocol</legend><div className="mt-2 grid grid-cols-2 gap-2">{(["oidc", "saml"] as const).map((value) => <label className={cn("cursor-pointer rounded-[12px] border p-4", protocol === value ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)]" : "border-[var(--ls-line)] bg-[var(--ls-surface-muted)]")} key={value}><input checked={protocol === value} className="sr-only" name="protocol" onChange={() => setProtocol(value)} type="radio" value={value} /><span className="text-sm font-semibold text-[var(--ls-text)]">{value.toUpperCase()}</span><span className="mt-1 block text-xs text-[var(--ls-text-secondary)]">{value === "oidc" ? "Discovery document and client ID" : "SAML IdP metadata document"}</span></label>)}</div></fieldset>
    <div className="mt-6 grid gap-4 sm:grid-cols-2"><Field defaultValue={configuration?.display_name} label="Display name" name="display_name" placeholder="Acme workforce SSO" required /><Field defaultValue={configuration?.group_claim ?? "groups"} label="Group claim" name="group_claim" placeholder="groups" required /></div>
    {protocol === "oidc" ? <div className="mt-4 grid gap-4"><Field defaultValue={configuration?.protocol === "oidc" ? configuration.issuer_url : undefined} label="Issuer URL" name="issuer_url" placeholder="https://identity.example.com" required type="url" /><Field defaultValue={configuration?.protocol === "oidc" ? configuration.client_id : undefined} label="Client ID" name="client_id" placeholder="open-review" required /></div> : <div className="mt-4"><Field defaultValue={configuration?.protocol === "saml" ? configuration.metadata_url : undefined} label="Metadata URL" name="metadata_url" placeholder="https://identity.example.com/saml/metadata" required type="url" /></div>}
    <div className="mt-4"><Field label="Secret reference" name="secret_ref" placeholder="secret://sso/acme or env://OPEN_REVIEW_SSO_SECRET_ACME" required={!configuration?.secret_configured} /><p className="mt-1.5 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">Use a secret-manager reference or an allowlisted environment reference. Never paste a raw client secret here.</p>{configuration?.secret_configured ? <label className="mt-3 flex items-center gap-2 text-xs text-[var(--ls-text-secondary)]"><input className="size-4 accent-[var(--ls-accent)]" defaultChecked name="keep_secret" type="checkbox" />Keep the currently configured secret reference</label> : null}</div>
    <div className="mt-7 flex flex-wrap justify-end gap-2"><button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] px-4 text-sm font-medium text-[var(--ls-text)] disabled:opacity-45" disabled={!configuration || pending === "probe" || configuration.state === "testing" || locked} onClick={onProbe} type="button">{pending === "probe" || configuration?.state === "testing" ? <LoaderCircle className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}Test current revision</button><button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45" disabled={locked || pending === "save"} type="submit">{pending === "save" ? <LoaderCircle className="size-4 animate-spin" /> : <Check className="size-4" />}Save new revision</button></div>
  </form><aside className="min-w-0 space-y-5"><section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><h3 className="text-sm font-semibold text-[var(--ls-text)]">Revision evidence</h3><dl className="mt-4 space-y-3 text-xs"><Metric label="Saved revision" value={configuration ? `r${configuration.revision}` : "—"} /><Metric label="Tested revision" value={configuration?.tested_revision ? `r${configuration.tested_revision}` : "—"} /><Metric label="Current revision valid" value={currentTested ? "Yes" : "No"} /></dl>{latestProbe ? <ProbeSummary probe={latestProbe} /> : <p className="mt-4 text-xs text-[var(--ls-text-tertiary)]">No probe receipt.</p>}</section><Boundary icon={Network} title="Probe safety">The worker fetches metadata over HTTPS with private, loopback, link-local, and unspecified addresses denied by default. The receipt is bound to one configuration revision.</Boundary><Boundary icon={KeyRound} title="No secret echo">The API only returns whether a secret reference exists. Changing the provider requires a new reference or an explicit keep-existing choice.</Boundary></aside></div>;
}

function DomainsAndMappings({ data, onAddDomain, onAddMapping, onDelete, onVerify, pending }: { data: SSOData; onAddDomain: (event: FormEvent<HTMLFormElement>) => void; onAddMapping: (event: FormEvent<HTMLFormElement>) => void; onDelete: (mapping: SSORoleMapping) => void; onVerify: (domain: SSODomain) => void; pending?: PendingAction }) {
  return <div className="grid grid-cols-[minmax(0,1fr)] gap-5 xl:grid-cols-2"><section className="min-w-0 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6"><div className="flex items-start gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><BadgeCheck className="size-5" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Verified domains</h2><HelpHint label="Verified domains">Prove workspace ownership with a server-observed DNS TXT record.</HelpHint></div></div></div><form className="mt-5 flex gap-2" onSubmit={onAddDomain}><input className="luminous-focus h-10 min-w-0 flex-1 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" name="domain" placeholder="example.com" required /><button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45" disabled={pending === "domain"} type="submit">{pending === "domain" ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}Add</button></form><div className="mt-5 space-y-3">{data.domains.length ? data.domains.map((domain) => <DomainCard domain={domain} key={domain.id} onVerify={() => onVerify(domain)} pending={pending === `verify:${domain.id}`} />) : <EmptyState icon={BadgeCheck} text="No domain challenges yet." />}</div></section>
  <section className="min-w-0 rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6"><div className="flex items-start gap-3"><span className="grid size-10 place-items-center rounded-[12px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]"><UserRoundCog className="size-5" /></span><div><div className="flex min-w-0 items-center gap-2"><h2 className="text-lg font-semibold text-[var(--ls-text)]">Group role mapping</h2><HelpHint label="Group role mapping">Map exact IdP group values to least-privilege workspace roles.</HelpHint></div></div></div><form className="mt-5 grid gap-3 sm:grid-cols-2" onSubmit={onAddMapping}><Field label="Group value" name="group_value" placeholder="engineering-platform" required /><label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">Workspace role</span><select className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)]" defaultValue="reviewer" name="role"><option value="admin">Admin</option><option value="rule_admin">Rule admin</option><option value="reviewer">Reviewer</option><option value="viewer">Viewer</option><option value="billing_viewer">Billing viewer</option></select></label><div className="sm:col-span-2"><Field defaultValue="*" label="Repository scope" name="repository_scope" placeholder="owner/repository or *" required /></div><button className="luminous-focus inline-flex h-10 items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 text-sm font-semibold text-white disabled:opacity-45 sm:col-span-2" disabled={pending === "mapping"} type="submit">{pending === "mapping" ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}Add mapping</button></form><div className="mt-5 space-y-2">{data.mappings.length ? data.mappings.map((mapping) => <div className="flex items-center gap-3 rounded-[13px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-3.5" key={mapping.id}><div className="min-w-0 flex-1"><p className="truncate font-mono text-xs font-semibold text-[var(--ls-text)]">{mapping.group_value}</p><p className="mt-1 text-[11px] text-[var(--ls-text-secondary)]">{mapping.role.replaceAll("_", " ")} · {mapping.repository_scope}</p></div><button aria-label={`Delete ${mapping.group_value}`} className="luminous-focus grid size-8 place-items-center rounded-[8px] text-[var(--ls-critical-text)] hover:bg-red-500/10" disabled={pending === `delete:${mapping.id}`} onClick={() => onDelete(mapping)} type="button">{pending === `delete:${mapping.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}</button></div>) : <EmptyState icon={UserRoundCog} text="No group mappings yet. Owner is intentionally unavailable here." />}</div></section></div>;
}

function DomainCard({ domain, onVerify, pending }: { domain: SSODomain; onVerify: () => void; pending: boolean }) { const record = `_open-review.${domain.domain}`; return <article className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex items-start justify-between gap-3"><div className="min-w-0"><div className="flex items-center gap-2"><p className="font-medium text-[var(--ls-text)]">{domain.domain}</p><span className={cn("rounded-full px-2 py-0.5 text-[10px] font-semibold", domain.state === "verified" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]")}>{domain.state}</span></div><p className="mt-1 text-[11px] text-[var(--ls-text-tertiary)]">TXT · {record}</p></div>{domain.state !== "verified" ? <button className="luminous-focus inline-flex h-8 shrink-0 items-center gap-1.5 rounded-[8px] border border-[var(--ls-line-strong)] px-2.5 text-xs font-medium text-[var(--ls-text)] disabled:opacity-45" disabled={pending} onClick={onVerify} type="button">{pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}Verify DNS</button> : <CheckCircle2 className="size-4 shrink-0 text-[var(--ls-success-text)]" />}</div><div className="mt-3 flex items-center gap-2 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-2.5"><code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap text-[11px] text-[var(--ls-text-secondary)]">{domain.challenge_token}</code><CopyButton value={domain.challenge_token} /></div></article>; }

function EnforcementSheet({ configuration, onClose, onSubmit, pending }: { configuration: SSOConfiguration; onClose: () => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void; pending: boolean }) { const dialogRef = useModalFocus<HTMLDivElement>(onClose); return <div aria-labelledby="enforce-sso-title" aria-modal="true" className="fixed inset-0 z-50 flex justify-end bg-black/30 backdrop-blur-[2px]" ref={dialogRef} role="dialog"><form className="luminous-frosted flex h-full w-full max-w-xl flex-col border-l border-[var(--ls-line-strong)] shadow-[var(--ls-shadow-float)]" onSubmit={onSubmit}><div className="border-b border-[var(--ls-line)] p-6"><p className="text-xs font-semibold uppercase tracking-[0.12em] text-[var(--ls-critical-text)]">Irreversible login-path change</p><h2 className="mt-2 text-xl font-semibold text-[var(--ls-text)]" id="enforce-sso-title">Enforce revision {configuration.revision}</h2><p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">Confirm an existing owner subject for emergency recovery. Editing the provider will remain locked until enforcement is suspended.</p></div><div className="flex-1 space-y-5 overflow-y-auto p-6"><div className="rounded-[13px] border border-amber-500/25 bg-amber-500/[0.06] p-4 text-xs leading-5 text-[var(--ls-text-secondary)]"><strong className="text-[var(--ls-warning-text)]">Before continuing:</strong> verify the current Casdoor federation adapter and recovery account in a separate private browser session. The metadata probe proves protocol reachability, not a successful human login.</div><Field label="Break-glass owner subject" name="break_glass_subject" placeholder="casdoor-subject-id" required /><label className="flex items-start gap-3 text-xs leading-5 text-[var(--ls-text-secondary)]"><input className="mt-0.5 size-4 accent-[var(--ls-accent)]" required type="checkbox" />I tested a full sign-in with the current revision and confirmed the named subject is an existing workspace owner.</label></div><div className="flex justify-end gap-2 border-t border-[var(--ls-line)] p-5"><button className="luminous-focus h-10 rounded-[10px] px-4 text-sm text-[var(--ls-text-secondary)]" onClick={onClose} type="button">Cancel</button><button className="luminous-focus inline-flex h-10 items-center gap-2 rounded-[10px] bg-red-600 px-4 text-sm font-semibold text-white disabled:opacity-45" disabled={pending} type="submit">{pending ? <LoaderCircle className="size-4 animate-spin" /> : <LockKeyhole className="size-4" />}Enforce SSO</button></div></form></div>; }

function ProbeSummary({ probe }: { probe: SSOProbeReceipt }) {
  const router = useRouter();
  const active = probe.state === "queued" || probe.state === "running";
  return <div className="mt-4 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"><div className="flex flex-wrap items-center gap-2"><ProbePill state={probe.state} /><span className="font-mono text-[11px] text-[var(--ls-text-tertiary)]">revision {probe.config_revision} · attempt {probe.attempt}</span>{active ? <button className="luminous-focus inline-flex h-7 items-center gap-1 rounded-[7px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-2 text-[10px] font-medium text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" onClick={() => router.refresh()} type="button"><RefreshCw className="size-3" />Refresh status</button> : null}</div>{active ? <p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">This durable probe continues after navigation. Refresh manually to inspect the latest result.</p> : null}<p className="mt-2 break-all text-xs text-[var(--ls-text-secondary)]">{probe.target_url}</p>{probe.metadata_sha256 ? <p className="mt-2 truncate font-mono text-[10px] text-[var(--ls-text-tertiary)]">sha256:{probe.metadata_sha256}</p> : null}{probe.error_message ? <p className="mt-2 text-xs leading-5 text-[var(--ls-critical-text)]">{probe.error_code}: {probe.error_message}</p> : null}</div>;
}
function ProbePill({ state }: { state: SSOProbeReceipt["state"] }) { const tone = state === "succeeded" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : state === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-amber-500/10 text-[var(--ls-warning-text)]"; return <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-semibold", tone)}>{state === "queued" || state === "running" ? <LoaderCircle className="size-3 animate-spin" /> : state === "succeeded" ? <Check className="size-3" /> : <CircleAlert className="size-3" />}{state}</span>; }
function StatePill({ state }: { state: SSOConfiguration["state"] }) { const tone = state === "enforced" || state === "enforcement_ready" || state === "verified" ? "bg-emerald-500/10 text-[var(--ls-success-text)]" : state === "testing" ? "bg-amber-500/10 text-[var(--ls-warning-text)]" : state === "suspended" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-[var(--ls-surface-muted)] text-[var(--ls-text-secondary)]"; return <span className={cn("inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-medium", tone)}><span className="size-1.5 rounded-full bg-current" />{stateLabels[state]}</span>; }
function Subtab({ href, label, selected }: { href: string; label: string; selected: boolean }) { return <Link aria-current={selected ? "page" : undefined} aria-selected={selected} className={cn("luminous-focus relative h-11 shrink-0 rounded-t-[10px] px-4 pt-3 text-sm font-medium", selected ? "text-[var(--ls-text)]" : "text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]")} href={href} role="tab" tabIndex={selected ? 0 : -1}>{label}{selected ? <span className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--ls-accent)]" /> : null}</Link>; }
function Field({ defaultValue, label, name, placeholder, required, type = "text" }: { defaultValue?: string; label: string; name: string; placeholder?: string; required?: boolean; type?: string }) { return <label className="block"><span className="mb-2 block text-xs font-medium text-[var(--ls-text-secondary)]">{label}</span><input className="luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] placeholder:text-[var(--ls-text-tertiary)] disabled:opacity-50" defaultValue={defaultValue} name={name} placeholder={placeholder} required={required} type={type} /></label>; }
function Notice({ text, tone }: { text: string; tone: "success" | "error" }) { return <div aria-live="polite" className={cn("flex items-start gap-2 rounded-[12px] border px-4 py-3 text-sm", tone === "success" ? "border-emerald-500/20 bg-emerald-500/[0.07] text-[var(--ls-success-text)]" : "border-red-500/20 bg-red-500/[0.07] text-[var(--ls-critical-text)]")}><CircleAlert className="mt-0.5 size-4 shrink-0" />{text}</div>; }
function Boundary({ icon: Icon, title, children }: { icon: typeof ShieldCheck; title: string; children: React.ReactNode }) { return <section className="rounded-[16px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5"><Icon className="size-4 text-[var(--ls-accent)]" /><h3 className="mt-3 text-sm font-semibold text-[var(--ls-text)]">{title}</h3><p className="mt-2 text-xs leading-5 text-[var(--ls-text-secondary)]">{children}</p></section>; }
function Metric({ label, value }: { label: string; value: string }) { return <div className="flex items-center justify-between gap-4"><dt className="text-[var(--ls-text-secondary)]">{label}</dt><dd className="font-medium text-[var(--ls-text)]">{value}</dd></div>; }
function EmptyState({ icon: Icon, text }: { icon: typeof BadgeCheck; text: string }) { return <div className="grid min-h-32 place-items-center rounded-[14px] border border-dashed border-[var(--ls-line-strong)] p-5 text-center"><div><Icon className="mx-auto size-5 text-[var(--ls-text-tertiary)]" /><p className="mt-2 text-xs text-[var(--ls-text-secondary)]">{text}</p></div></div>; }
function Unavailable({ detail }: { detail?: string }) { return <PageState className="min-h-72" detail={detail ?? "The SSO state could not be loaded."} kind="unavailable" title="SSO control plane unavailable" />; }
function CopyButton({ value }: { value: string }) { const [copied, setCopied] = useState(false); async function copy() { await navigator.clipboard.writeText(value); setCopied(true); window.setTimeout(() => setCopied(false), 1400); } return <button aria-label="Copy TXT value" className="luminous-focus grid size-7 shrink-0 place-items-center rounded-[7px] text-[var(--ls-text-secondary)] hover:bg-[var(--ls-surface-muted)]" onClick={copy} type="button">{copied ? <Check className="size-3.5 text-[var(--ls-success-text)]" /> : <Clipboard className="size-3.5" />}</button>; }
function jsonRequest(method: string, body: unknown): RequestInit { return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }; }
