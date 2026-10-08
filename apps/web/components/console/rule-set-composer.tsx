"use client";

import { useWorkflowText } from "@/components/console/ui-language-context";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { CirclePlus, LoaderCircle, ShieldCheck, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";

type Enforcement = "mandatory" | "advisory";
type Severity = "low" | "medium" | "high" | "critical";

type DraftRule = {
  id: number;
  key: string;
  enforcement: Enforcement;
  severity: Severity;
  prompt: string;
};

const inputClassName =
  "luminous-focus h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 text-sm text-[var(--ls-text)] outline-none transition-colors placeholder:text-[var(--ls-text-tertiary)] disabled:cursor-not-allowed disabled:opacity-50";

const textAreaClassName =
  "luminous-focus min-h-24 w-full resize-y rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3 py-2.5 text-sm leading-6 text-[var(--ls-text)] outline-none transition-colors placeholder:text-[var(--ls-text-tertiary)] disabled:cursor-not-allowed disabled:opacity-50";

let nextDraftRuleID = 2;

function newDraftRule(): DraftRule {
  const id = nextDraftRuleID;
  nextDraftRuleID += 1;
  return {
    id,
    key: "security.review-" + id,
    enforcement: "advisory",
    severity: "medium",
    prompt: "",
  };
}

export function RuleSetComposer({
  enabled,
  org,
}: {
  enabled: boolean;
  org: string;
}) {
  const t = useWorkflowText();
  const router = useRouter();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [rules, setRules] = useState<DraftRule[]>([
    {
      id: 1,
      key: "security.no-credential-leak",
      enforcement: "mandatory",
      severity: "high",
      prompt:
        t("Identify whether this change exposes credentials, tokens, private keys, or personally identifiable data."),
    },
  ]);
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string>();

  function updateRule(id: number, changes: Partial<DraftRule>) {
    setRules((current) =>
      current.map((rule) => (rule.id === id ? { ...rule, ...changes } : rule)),
    );
  }

  async function createRuleSet(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enabled || pending) return;

    if (
      !name.trim() ||
      rules.some((rule) => !rule.key.trim() || !rule.prompt.trim())
    ) {
      setMessage(t("Name, rule key, and review instruction are required."));
      return;
    }

    setPending(true);
    setMessage(undefined);
    try {
      const response = await fetch(
        "/api/tenants/" + encodeURIComponent(org) + "/rule-sets",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            name,
            description,
            rules: rules.map((rule) => ({
              key: rule.key.trim(),
              enforcement: rule.enforcement,
              merge_behavior:
                rule.enforcement === "mandatory" ? "deny_override" : "replace",
              severity: rule.severity,
              content: { prompt: rule.prompt.trim() },
            })),
          }),
        },
      );
      const payload = (await response.json().catch(() => ({}))) as {
        error?: string;
        draft?: { version?: number };
      };
      if (!response.ok) {
        throw new Error(payload.error ?? t("The rule set was not accepted."));
      }
      setName("");
      setDescription("");
      setRules([newDraftRule()]);
      setMessage(
        t("Draft version {version} was created. Request governed approval before publishing it.", { version: payload.draft?.version ?? 1 }),
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : t("The rule set could not be created."),
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="rounded-[18px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-[var(--ls-text)]">
            <span className="grid size-8 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <ShieldCheck className="size-4" />
            </span>
            {t(" New policy draft ")}</div>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ls-text-secondary)]">
            {t(" Compose structured review rules. The server validates the normalized rules before storing an immutable draft; publication still requires governed approval. ")}</p>
        </div>
        <span className="inline-flex w-fit rounded-full bg-[var(--ls-accent-soft)] px-2.5 py-1 text-[11px] font-medium text-[var(--ls-accent)]">
          {t(" Governance draft ")}</span>
      </div>

      <form className="mt-6 space-y-5" onSubmit={createRuleSet}>
        <div className="grid gap-4 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
          <label className="space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Policy name ")}<input
              className={inputClassName}
              disabled={!enabled || pending}
              onChange={(event) => setName(event.target.value)}
              placeholder={t("Credential boundary")}
              required
              value={name}
            />
          </label>
          <label className="space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
            {t(" Purpose ")}<input
              className={inputClassName}
              disabled={!enabled || pending}
              onChange={(event) => setDescription(event.target.value)}
              placeholder={t("Explain the intent and expected review behavior.")}
              value={description}
            />
          </label>
        </div>

        <div className="space-y-3">
          {rules.map((rule, index) => (
            <fieldset
              className="rounded-[14px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] p-4"
              key={rule.id}
            >
              <div className="flex items-center justify-between gap-3">
                <legend className="text-xs font-medium text-[var(--ls-text)]">
                  {t(" Rule ")}{index + 1}
                </legend>
                <Button
                  aria-label={t("Remove rule {number}", { number: index + 1 })}
                  disabled={!enabled || pending || rules.length === 1}
                  onClick={() =>
                    setRules((current) =>
                      current.filter((candidate) => candidate.id !== rule.id),
                    )
                  }
                  size="icon-xs"
                  type="button"
                  variant="ghost"
                >
                  <Trash2 />
                </Button>
              </div>
              <div className="mt-3 grid gap-3 sm:grid-cols-3">
                <label className="space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)] sm:col-span-1">
                  {t(" Stable key ")}<input
                    className={inputClassName}
                    disabled={!enabled || pending}
                    onChange={(event) =>
                      updateRule(rule.id, { key: event.target.value })
                    }
                    placeholder="security.no-secrets"
                    required
                    value={rule.key}
                  />
                </label>
                <label className="space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
                  {t(" Enforcement ")}<select
                    className={inputClassName}
                    disabled={!enabled || pending}
                    onChange={(event) =>
                      updateRule(rule.id, {
                        enforcement: event.target.value as Enforcement,
                      })
                    }
                    value={rule.enforcement}
                  >
                    <option value="mandatory">{t("Mandatory")}</option>
                    <option value="advisory">{t("Advisory")}</option>
                  </select>
                </label>
                <label className="space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
                  {t(" Severity ")}<select
                    className={inputClassName}
                    disabled={!enabled || pending}
                    onChange={(event) =>
                      updateRule(rule.id, {
                        severity: event.target.value as Severity,
                      })
                    }
                    value={rule.severity}
                  >
                    <option value="low">{t("Low")}</option>
                    <option value="medium">{t("Medium")}</option>
                    <option value="high">{t("High")}</option>
                    <option value="critical">{t("Critical")}</option>
                  </select>
                </label>
              </div>
              <label className="mt-3 block space-y-1.5 text-xs font-medium text-[var(--ls-text-secondary)]">
                {t(" Review instruction ")}<textarea
                  className={textAreaClassName}
                  disabled={!enabled || pending}
                  onChange={(event) =>
                    updateRule(rule.id, { prompt: event.target.value })
                  }
                  placeholder={t("State the failure condition, evidence to inspect, and expected finding.")}
                  required
                  value={rule.prompt}
                />
              </label>
              <p className="mt-2 text-[11px] leading-5 text-[var(--ls-text-tertiary)]">
                {rule.enforcement === "mandatory"
                  ? t("Mandatory rules use deny_override, so lower-precedence policies cannot weaken them.")
                  : t("Advisory rules replace lower-precedence versions with the same stable key.")}
              </p>
            </fieldset>
          ))}
        </div>

        <div className="flex flex-col justify-between gap-3 border-t border-[var(--ls-line)] pt-4 sm:flex-row sm:items-center">
          <Button
            disabled={!enabled || pending}
            onClick={() => setRules((current) => [...current, newDraftRule()])}
            size="sm"
            type="button"
            variant="outline"
          >
            <CirclePlus />
            {t(" Add rule ")}</Button>
          <Button disabled={!enabled || pending} size="sm" type="submit">
            {pending ? (
              <>
                <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                {t(" Creating draft ")}</>
            ) : (
              t("Create draft")
            )}
          </Button>
        </div>
      </form>
      {!enabled ? (
        <p className="mt-4 rounded-[12px] border border-amber-500/20 bg-amber-500/[0.07] px-3 py-2 text-xs leading-5 text-[var(--ls-warning-text)]">
          {t(" The control plane is not reachable with this signed-in session. Check the web service CONTROL_API_URL and the assigned tenant role. ")}</p>
      ) : null}
      {message ? (
        <p
          aria-live="polite"
          className="mt-4 text-xs leading-5 text-[var(--ls-accent)]"
        >
          {message}
        </p>
      ) : null}
    </section>
  );
}
