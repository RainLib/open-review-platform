"use client";

import { useWorkflowText } from "@/components/console/ui-language-context";

import { useState } from "react";
import { useRouter } from "next/navigation";
import type { WorkspaceApprovalPolicy } from "@/lib/control-api";

export function ApprovalPolicyManager({ org, initialPolicy }: { org: string; initialPolicy: WorkspaceApprovalPolicy }) {
  const t = useWorkflowText();
  const router = useRouter();
  const [policy, setPolicy] = useState(initialPolicy);
  const [agent, setAgent] = useState(initialPolicy.allow_agent_plan_self_approval);
  const [rules, setRules] = useState(initialPolicy.allow_rule_self_approval);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const dirty = agent !== policy.allow_agent_plan_self_approval || rules !== policy.allow_rule_self_approval;

  async function save() {
    setBusy(true); setError(""); setMessage("");
    try {
      const response = await fetch(`/api/tenants/${encodeURIComponent(org)}/approval-policy`, {
        method: "PUT", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ allow_agent_plan_self_approval: agent, allow_rule_self_approval: rules, expected_revision: policy.revision }),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || t("Could not save approval settings."));
      setPolicy(result); setAgent(result.allow_agent_plan_self_approval); setRules(result.allow_rule_self_approval);
      setMessage(t("Approval settings saved (revision {revision}).", {revision: result.revision}));
      router.refresh();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("Could not save approval settings.")); }
    finally { setBusy(false); }
  }

  return <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-6">
    <h2 className="text-base font-semibold">{t("Author self-approval")}</h2>
    <p className="mt-2 text-sm leading-6 text-[var(--ls-text-secondary)]">{t("Disabled by default. The workspace owner can enable this setting. Authors with the required role can then manually approve their own requests. Every setting change and approval is audited.")}</p>
    <fieldset className="mt-5 space-y-4" disabled={busy || !policy.can_update}>
      <label className="flex items-start gap-3"><input className="mt-1 accent-violet-600" type="checkbox" checked={agent} onChange={(event) => setAgent(event.target.checked)} /><span><span className="text-sm font-medium">{t("Allow Agent plan authors to approve their own plans")}</span><span className="mt-1 block text-xs text-[var(--ls-text-secondary)]">{t("Only owners or admins can approve; the exact plan revision and acceptance criteria must still be confirmed.")}</span></span></label>
      <label className="flex items-start gap-3"><input className="mt-1 accent-violet-600" type="checkbox" checked={rules} onChange={(event) => setRules(event.target.checked)} /><span><span className="text-sm font-medium">{t("Allow rule version requesters to approve their own requests")}</span><span className="mt-1 block text-xs text-[var(--ls-text-secondary)]">{t("The rule approval role is still required. One vote per person, approval counts and content hashes remain enforced.")}</span></span></label>
    </fieldset>
    <p className="mt-5 text-xs leading-5 text-[var(--ls-text-secondary)]">{t("These settings apply only to Agent plans and rule versions. Disabling blocks new self-approvals and preserves existing approvals. Model configuration, exceptions, rollouts and data governance still require a different approver.")}</p>
    <div className="mt-5 flex items-center gap-4"><button className="luminous-focus rounded-[10px] bg-[var(--ls-accent)] px-4 py-2 text-sm font-medium text-white disabled:opacity-45" disabled={busy || !policy.can_update || !dirty} onClick={save} type="button">{busy ? t("Saving…") : t("Save approval settings")}</button><span className="text-xs text-[var(--ls-text-secondary)]">{t("Version")} {policy.revision}{policy.updated_by ? ` · ${policy.updated_by}` : ` · ${t("Default configuration")}`}</span></div>
    {!policy.can_update ? <p className="mt-3 text-sm">{t("Only the workspace owner can change approval settings.")}</p> : null}
    {message ? <p className="mt-3 text-sm text-[var(--ls-success-text)]" role="status">{message}</p> : null}
    {error ? <p className="mt-3 text-sm text-[var(--ls-critical-text)]" role="alert">{error}</p> : null}
  </section>;
}
