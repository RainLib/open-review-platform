"use client";

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
  "h-9 w-full rounded-xl border border-white/10 bg-black/20 px-3 text-sm text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-cyan-300/50 focus:ring-2 focus:ring-cyan-300/10 disabled:cursor-not-allowed disabled:opacity-50";

const textAreaClassName =
  "min-h-24 w-full resize-y rounded-xl border border-white/10 bg-black/20 px-3 py-2.5 text-sm leading-6 text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-cyan-300/50 focus:ring-2 focus:ring-cyan-300/10 disabled:cursor-not-allowed disabled:opacity-50";

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
        "Identify whether this change exposes credentials, tokens, private keys, or personally identifiable data.",
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
      setMessage("Name, rule key, and review instruction are required.");
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
        throw new Error(payload.error ?? "The rule set was not accepted.");
      }
      setName("");
      setDescription("");
      setRules([newDraftRule()]);
      setMessage(
        "Draft version " +
          (payload.draft?.version ?? 1) +
          " was created. Request independent approval before publishing it.",
      );
      router.refresh();
    } catch (error) {
      setMessage(
        error instanceof Error
          ? error.message
          : "The rule set could not be created.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="rounded-2xl border border-white/[0.075] bg-console-surface p-5 sm:p-6">
      <div className="flex flex-col justify-between gap-4 sm:flex-row sm:items-start">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
            <span className="grid size-8 place-items-center rounded-xl bg-violet-300/[0.09] text-violet-200">
              <ShieldCheck className="size-4" />
            </span>
            New policy draft
          </div>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-zinc-500">
            Compose structured review rules. The server validates the
            normalized rules before storing an immutable draft; publication
            still requires independent approval.
          </p>
        </div>
        <span className="inline-flex w-fit rounded-full border border-violet-300/15 bg-violet-300/[0.06] px-2.5 py-1 text-[11px] font-medium text-violet-200">
          Governance draft
        </span>
      </div>

      <form className="mt-6 space-y-5" onSubmit={createRuleSet}>
        <div className="grid gap-4 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
          <label className="space-y-1.5 text-xs font-medium text-zinc-400">
            Policy name
            <input
              className={inputClassName}
              disabled={!enabled || pending}
              onChange={(event) => setName(event.target.value)}
              placeholder="Credential boundary"
              required
              value={name}
            />
          </label>
          <label className="space-y-1.5 text-xs font-medium text-zinc-400">
            Purpose
            <input
              className={inputClassName}
              disabled={!enabled || pending}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Explain the intent and expected review behavior."
              value={description}
            />
          </label>
        </div>

        <div className="space-y-3">
          {rules.map((rule, index) => (
            <fieldset
              className="rounded-xl border border-white/[0.06] bg-black/15 p-4"
              key={rule.id}
            >
              <div className="flex items-center justify-between gap-3">
                <legend className="text-xs font-medium text-zinc-300">
                  Rule {index + 1}
                </legend>
                <Button
                  aria-label={"Remove rule " + (index + 1)}
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
                <label className="space-y-1.5 text-xs font-medium text-zinc-400 sm:col-span-1">
                  Stable key
                  <input
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
                <label className="space-y-1.5 text-xs font-medium text-zinc-400">
                  Enforcement
                  <select
                    className={inputClassName}
                    disabled={!enabled || pending}
                    onChange={(event) =>
                      updateRule(rule.id, {
                        enforcement: event.target.value as Enforcement,
                      })
                    }
                    value={rule.enforcement}
                  >
                    <option value="mandatory">Mandatory</option>
                    <option value="advisory">Advisory</option>
                  </select>
                </label>
                <label className="space-y-1.5 text-xs font-medium text-zinc-400">
                  Severity
                  <select
                    className={inputClassName}
                    disabled={!enabled || pending}
                    onChange={(event) =>
                      updateRule(rule.id, {
                        severity: event.target.value as Severity,
                      })
                    }
                    value={rule.severity}
                  >
                    <option value="low">Low</option>
                    <option value="medium">Medium</option>
                    <option value="high">High</option>
                    <option value="critical">Critical</option>
                  </select>
                </label>
              </div>
              <label className="mt-3 block space-y-1.5 text-xs font-medium text-zinc-400">
                Review instruction
                <textarea
                  className={textAreaClassName}
                  disabled={!enabled || pending}
                  onChange={(event) =>
                    updateRule(rule.id, { prompt: event.target.value })
                  }
                  placeholder="State the failure condition, evidence to inspect, and expected finding."
                  required
                  value={rule.prompt}
                />
              </label>
              <p className="mt-2 text-[11px] leading-5 text-zinc-500">
                {rule.enforcement === "mandatory"
                  ? "Mandatory rules use deny_override, so lower-precedence policies cannot weaken them."
                  : "Advisory rules replace lower-precedence versions with the same stable key."}
              </p>
            </fieldset>
          ))}
        </div>

        <div className="flex flex-col justify-between gap-3 border-t border-white/[0.06] pt-4 sm:flex-row sm:items-center">
          <Button
            disabled={!enabled || pending}
            onClick={() => setRules((current) => [...current, newDraftRule()])}
            size="sm"
            type="button"
            variant="outline"
          >
            <CirclePlus />
            Add rule
          </Button>
          <Button disabled={!enabled || pending} size="sm" type="submit">
            {pending ? (
              <>
                <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                Creating draft
              </>
            ) : (
              "Create draft"
            )}
          </Button>
        </div>
      </form>
      {!enabled ? (
        <p className="mt-4 rounded-xl border border-amber-300/15 bg-amber-300/[0.045] px-3 py-2 text-xs leading-5 text-amber-100/80">
          Enable a local development control-plane bridge to create drafts.
          Production writes require the Casdoor session bridge.
        </p>
      ) : null}
      {message ? (
        <p
          aria-live="polite"
          className="mt-4 text-xs leading-5 text-cyan-100/85"
        >
          {message}
        </p>
      ) : null}
    </section>
  );
}
