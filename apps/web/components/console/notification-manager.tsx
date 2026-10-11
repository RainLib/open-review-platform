"use client";

import { SectionDisclosure } from "./section-disclosure";

import { useWorkflowText } from "./ui-language-context";

import { FormEvent, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowDown,
  ArrowUp,
  BellRing,
  CheckCircle2,
  CircleAlert,
  GitBranch,
  LoaderCircle,
  MessageCircleMore,
  Power,
  RotateCcw,
  Route,
  Send,
  Webhook,
} from "lucide-react";

import type {
  NotificationDestination,
  NotificationDelivery,
  NotificationRoute,
  NotificationRoutePreview,
  DataSource,
} from "@/lib/control-api";
import { PageState, RecoveryAction } from "@/components/console/page-state";
import { cn } from "@/lib/utils";
import { HelpHint } from "@/components/console/help-hint";

const providerPresentation = {
  dingtalk: { label: "DingTalk", icon: MessageCircleMore, tone: "text-sky-500" },
  feishu: { label: "Feishu", icon: Send, tone: "text-blue-500" },
  slack: { label: "Slack", icon: MessageCircleMore, tone: "text-fuchsia-500" },
  webhook: { label: "Webhook", icon: Webhook, tone: "text-violet-500" },
} as const;

const terminalEvents = [
  { value: "review.run.completed", label: "Completed", defaultChecked: true },
  { value: "review.run.needs_attention", label: "Needs attention", defaultChecked: true },
  { value: "review.run.failed", label: "Failed", defaultChecked: true },
  { value: "review.run.cancelled", label: "Cancelled", defaultChecked: false },
  { value: "review.run.superseded", label: "Superseded", defaultChecked: false },
] as const;

const credentialSlots = [
  { value: "env:OPENREVIEW_NOTIFY_PLATFORM", label: "Platform alerts" },
  { value: "env:OPENREVIEW_NOTIFY_ENGINEERING", label: "Engineering" },
  { value: "env:OPENREVIEW_NOTIFY_SECURITY", label: "Security" },
  { value: "env:OPENREVIEW_NOTIFY_INCIDENTS", label: "Incidents" },
  { value: "env:OPENREVIEW_NOTIFY_RELEASES", label: "Releases" },
  { value: "env:OPENREVIEW_NOTIFY_PRODUCT", label: "Product" },
] as const;

const deliveryDateFormatter = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

type Props = {
  detail?: string;
  org: string;
  enabled: boolean;
  destinations: NotificationDestination[];
  routes: NotificationRoute[];
  deliveries: NotificationDelivery[];
  source: DataSource;
  view: "destinations" | "routing" | "deliveries";
};

export function NotificationManager({
  detail,
  org,
  enabled,
  destinations,
  routes,
  deliveries,
  source,
  view,
}: Props) {
  const t = useWorkflowText();
  const router = useRouter();
  const [busy, setBusy] = useState<string>();
  const [message, setMessage] = useState<string>();
  const [provider, setProvider] = useState<keyof typeof providerPresentation>("feishu");
  const [destinationID, setDestinationID] = useState(destinations[0]?.id ?? "");
  const [routePreview, setRoutePreview] = useState<NotificationRoutePreview>();
  const destinationNames = useMemo(
    () => new Map(destinations.map((destination) => [destination.id, destination.name])),
    [destinations],
  );
  const routePreviewCounts = useMemo(() => ({
    selected: routePreview?.matches.filter(({ disposition }) => disposition === "selected").length ?? 0,
    filtered: routePreview?.matches.filter(({ disposition }) => disposition === "filtered").length ?? 0,
    deduplicated: routePreview?.matches.filter(({ disposition }) => disposition === "deduplicated").length ?? 0,
  }), [routePreview]);

  function requireEnabled() {
    if (enabled) return true;
    setMessage("Preview data is read-only. Connect a live control plane before changing notification delivery.");
    return false;
  }

  async function submitDestination(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
	if (!requireEnabled()) return;
    const formElement = event.currentTarget;
    setBusy("destination");
    setMessage(undefined);
    const form = new FormData(formElement);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-destinations`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: form.get("name"),
          provider,
          secret_ref: form.get("secret_ref"),
          enabled: true,
        }),
      },
    );
    if (!response.ok) {
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      setMessage(body.error ?? "The destination could not be created.");
    } else {
      setMessage("Destination created. Add a repository route to begin delivery.");
      formElement.reset();
      router.refresh();
    }
    setBusy(undefined);
  }

  async function submitRoute(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
	if (!requireEnabled()) return;
    const formElement = event.currentTarget;
    setBusy("route");
    setMessage(undefined);
    setRoutePreview(undefined);
    const form = new FormData(formElement);
    const eventTypes = terminalEvents
      .filter(({ value }) => form.get(value) === "on")
      .map(({ value }) => value);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-routes`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          destination_id: destinationID,
          repository_glob: form.get("repository_glob"),
          branch_glob: form.get("branch_glob"),
          event_types: eventTypes,
          min_severity: form.get("min_severity"),
          enabled: true,
        }),
      },
    );
    if (!response.ok) {
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      setMessage(body.error ?? "The route could not be created.");
    } else {
      setMessage("Route enabled. Matching terminal review events will be delivered asynchronously.");
      formElement.reset();
      router.refresh();
    }
    setBusy(undefined);
  }

  async function previewRoute(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
	if (!requireEnabled()) return;
    setBusy("route-preview");
    setMessage(undefined);
    const form = new FormData(event.currentTarget);
    const findingCount = Number(form.get("finding_count"));
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-routes/preview`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          repository: form.get("repository"),
          target_branch: form.get("target_branch"),
          event_type: form.get("event_type"),
          highest_severity: findingCount === 0 ? "none" : form.get("highest_severity"),
          finding_count: findingCount,
        }),
      },
    );
    const body = (await response.json().catch(() => ({}))) as {
      error?: string;
      notification_route_preview?: NotificationRoutePreview;
    };
    if (response.ok && body.notification_route_preview) {
      setRoutePreview(body.notification_route_preview);
      setMessage("Route preview complete. No message was queued or sent.");
    } else {
      setRoutePreview(undefined);
      setMessage(body.error ?? "The notification route could not be previewed.");
    }
    setBusy(undefined);
  }

  async function toggleDestination(destination: NotificationDestination) {
	if (!requireEnabled()) return;
    setBusy(`destination:${destination.id}`);
    setMessage(undefined);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-destinations/${encodeURIComponent(destination.id)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          enabled: !destination.enabled,
          expected_revision: destination.revision,
        }),
      },
    );
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `Destination ${destination.enabled ? "paused" : "enabled"}.` : body.error ?? "The destination could not be updated.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function sendDestinationTest(destination: NotificationDestination) {
	if (!requireEnabled()) return;
    setBusy(`test:${destination.id}`);
    setMessage(undefined);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-destinations/${encodeURIComponent(destination.id)}/test`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: destination.revision }),
      },
    );
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(
      response.ok
        ? `Test queued for ${destination.name}. The final provider result will appear in Delivery logs.`
        : body.error ?? "The notification test could not be queued.",
    );
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function toggleRoute(route: NotificationRoute) {
	if (!requireEnabled()) return;
    setBusy(`route:${route.id}`);
    setMessage(undefined);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-routes/${encodeURIComponent(route.id)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          enabled: !route.enabled,
          expected_revision: route.revision,
        }),
      },
    );
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? `Route ${route.enabled ? "paused" : "enabled"}.` : body.error ?? "The route could not be updated.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function moveRoute(routeID: string, direction: -1 | 1) {
    if (!requireEnabled()) return;
    const currentIndex = routes.findIndex((route) => route.id === routeID);
    const targetIndex = currentIndex + direction;
    if (currentIndex < 0 || targetIndex < 0 || targetIndex >= routes.length) return;

    const orderedRoutes = [...routes];
    [orderedRoutes[currentIndex], orderedRoutes[targetIndex]] = [orderedRoutes[targetIndex], orderedRoutes[currentIndex]];
    setBusy("route-order");
    setMessage(undefined);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-routes/reorder`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          routes: orderedRoutes.map((route) => ({ id: route.id, expected_revision: route.revision })),
        }),
      },
    );
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? "Route priority updated. Future delivery and preview now use this order." : body.error ?? "The route order could not be updated.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  async function retryDelivery(delivery: NotificationDelivery) {
	if (!requireEnabled()) return;
    setBusy(`delivery:${delivery.id}`);
    setMessage(undefined);
    const response = await fetch(
      `/api/tenants/${encodeURIComponent(org)}/notification-deliveries/${encodeURIComponent(delivery.id)}/retry`,
      { method: "POST" },
    );
    const body = (await response.json().catch(() => ({}))) as { error?: string };
    setMessage(response.ok ? "Retry queued. Delivery will run asynchronously." : body.error ?? "The delivery could not be retried.");
    setBusy(undefined);
    if (response.ok) router.refresh();
  }

  if (source === "unavailable") {
    return (
      <PageState
        detail={detail ?? "Notification destinations, routes, and delivery evidence could not be read. No delivery mutation was attempted."}
        kind="unavailable"
        title="Notification control plane is temporarily unavailable"
      />
    );
  }

  if (source === "unconfigured") {
    return (
      <PageState
        action={(
          <RecoveryAction href={`/${encodeURIComponent(org)}/connect?tab=installations`} variant="primary">
            Connect control plane
          </RecoveryAction>
        )}
        detail={detail ?? "Connect a live control plane before registering a secret reference, routing review events, or viewing delivery evidence."}
        kind="first-use-empty"
        title="Notifications need a connected control plane"
      />
    );
  }

  return (
    <div className="space-y-6">
      {message ? (
        <div className="flex items-start gap-2 rounded-[12px] border border-[color:color-mix(in_srgb,var(--ls-accent)_26%,transparent)] bg-[var(--ls-accent-soft)] px-4 py-3 text-sm text-[var(--ls-text-secondary)]">
          <CircleAlert className="mt-0.5 size-4 shrink-0 text-[var(--ls-accent)]" />
          {message}
        </div>
      ) : null}

      {!enabled ? <section className="rounded-[14px] border border-amber-500/25 bg-amber-500/[0.06] px-4 py-3 text-sm leading-6 text-[var(--ls-warning-text)]">{source === "demo" ? "Preview data is read-only. It can illustrate destinations, routing, and delivery evidence, but cannot create, pause, test, preview, or retry a notification." : "Notifications are read-only until a live control plane is connected."}</section> : null}
      <fieldset className="min-w-0 space-y-5" disabled={!enabled}>
      <div className="grid gap-4">
        {view === "destinations" ? (
        <SectionDisclosure title={t("Add a destination")} ><form className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={submitDestination}>
          <div className="flex items-start gap-3">
            <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <BellRing className="size-4" />
            </span>
            <div>
              <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">1. Add a destination</h2><HelpHint label="1. Add a destination">Credentials stay in deployment environment variables; the database only stores an env reference.</HelpHint></div>
            </div>
          </div>
          <div className="mt-5 grid grid-cols-2 gap-2 sm:grid-cols-4">
            {(Object.keys(providerPresentation) as Array<keyof typeof providerPresentation>).map((value) => {
              const item = providerPresentation[value];
              const Icon = item.icon;
              return (
                <button
                  className={cn("luminous-focus flex items-center justify-center gap-2 rounded-[10px] border px-3 py-2.5 text-xs font-medium transition", provider === value ? "border-[var(--ls-accent)] bg-[var(--ls-accent-soft)] text-[var(--ls-text)]" : "border-[var(--ls-line)] text-[var(--ls-text-secondary)] hover:border-[var(--ls-line-strong)] hover:text-[var(--ls-text)]")}
                  key={value}
                  onClick={() => setProvider(value)}
                  type="button"
                >
                  <Icon className={cn("size-4", item.tone)} /> {item.label}
                </button>
              );
            })}
          </div>
          <label className="mt-5 block text-xs font-medium text-[var(--ls-text-secondary)]">
            Display name
            <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none transition placeholder:text-[var(--ls-text-tertiary)]" name="name" placeholder="Platform alerts" required />
          </label>
          <label className="mt-4 block text-xs font-medium text-[var(--ls-text-secondary)]">
            Credential reference
            <input aria-describedby="notification-credential-help" className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 font-mono text-sm text-[var(--ls-text)] outline-none transition placeholder:text-[var(--ls-text-tertiary)]" defaultValue="env:OPENREVIEW_NOTIFY_PLATFORM" list="notification-credential-slots" name="secret_ref" pattern="env:[A-Z0-9_]+" placeholder="env:OPENREVIEW_NOTIFY_PLATFORM" required />
            <datalist id="notification-credential-slots">
              {credentialSlots.map((slot) => <option key={slot.value} value={slot.value}>{slot.label}</option>)}
            </datalist>
          </label>
          <p className="mt-2 text-xs leading-5 text-[var(--ls-text-tertiary)]" id="notification-credential-help">Standard self-hosted slots are suggested above. A custom <code>env:NAME</code> works only when that exact variable is explicitly passed to the notifier service.</p>
          <button className="luminous-focus mt-5 flex w-full items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 py-2.5 text-sm font-semibold text-white transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-45" disabled={!enabled || busy !== undefined} type="submit">
            {busy === "destination" ? <LoaderCircle className="size-4 animate-spin" /> : null}
            Create destination
          </button>
        </form></SectionDisclosure>
        ) : null}

        {view === "routing" ? (
        <SectionDisclosure title={t("Route repository events")} ><form className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={submitRoute}>
          <div className="flex items-start gap-3">
            <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]">
              <Route className="size-4" />
            </span>
            <div>
              <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">2. Route repository events</h2><HelpHint label="2. Route repository events">Fan out different repositories and severities to the right team channel.</HelpHint></div>
            </div>
          </div>
          <label className="mt-5 block text-xs font-medium text-[var(--ls-text-secondary)]">
            Destination
            <select className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none" onChange={(event) => setDestinationID(event.target.value)} required value={destinationID}>
              <option value="">Select a destination</option>
              {destinations.map((destination) => <option key={destination.id} value={destination.id}>{destination.name}</option>)}
            </select>
          </label>
          <div className="mt-4 grid gap-3 sm:grid-cols-3">
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Repository scope
              <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 font-mono text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" name="repository_glob" placeholder="RainLib/*" required />
            </label>
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Target branch
              <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 font-mono text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" defaultValue="*" name="branch_glob" placeholder="release/*" required />
            </label>
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Minimum severity
              <select className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="medium" name="min_severity">
                <option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option>
              </select>
            </label>
          </div>
          <fieldset className="mt-4">
            <legend className="text-xs font-medium text-[var(--ls-text-secondary)]">Notify when</legend>
            <div className="mt-2 grid gap-2 sm:grid-cols-3">
              {terminalEvents.map(({ value, label, defaultChecked }) => (
                <label className="flex items-center gap-2 rounded-[10px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-3 py-2.5 text-xs text-[var(--ls-text-secondary)]" key={value}>
                  <input className="size-4 accent-[var(--ls-accent)]" defaultChecked={defaultChecked} name={value} type="checkbox" /> {label}
                </label>
              ))}
            </div>
          </fieldset>
          <button className="luminous-focus mt-5 flex w-full items-center justify-center gap-2 rounded-[10px] bg-[var(--ls-accent)] px-4 py-2.5 text-sm font-semibold text-white shadow-[var(--ls-shadow-control)] transition hover:bg-[var(--ls-accent-hover)] disabled:cursor-not-allowed disabled:opacity-35" disabled={!enabled || !destinationID || busy !== undefined} type="submit">
            {busy === "route" ? <LoaderCircle className="size-4 animate-spin" /> : null}
            Enable route
          </button>
        </form></SectionDisclosure>
        ) : null}

        {view === "routing" ? (
        <SectionDisclosure title={t("Preview delivery")} ><form className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6" onSubmit={previewRoute}>
          <div className="flex items-start gap-3">
            <span className="grid size-9 place-items-center rounded-[10px] bg-[var(--ls-surface-muted)] text-[var(--ls-accent)]">
              <Route className="size-4" />
            </span>
            <div>
              <div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Preview delivery</h2><HelpHint label="Preview delivery">See which saved route wins for a destination before a real review event arrives. This never sends a message.</HelpHint></div>
            </div>
          </div>
          <div className="mt-5 grid gap-3 sm:grid-cols-2">
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Repository
              <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 font-mono text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" defaultValue="RainLib/open-review-platform" name="repository" required />
            </label>
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Target branch
              <input className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 font-mono text-sm text-[var(--ls-text)] outline-none placeholder:text-[var(--ls-text-tertiary)]" defaultValue="main" name="target_branch" required />
            </label>
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Terminal event
              <select className="luminous-focus mt-2 h-10 w-full rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="review.run.completed" name="event_type">
                {terminalEvents.map(({ value, label }) => <option key={value} value={value}>{label}</option>)}
              </select>
            </label>
            <label className="text-xs font-medium text-[var(--ls-text-secondary)]">
              Findings / highest severity
              <div className="mt-2 grid grid-cols-[86px_minmax(0,1fr)] gap-2">
                <input className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="1" min="0" name="finding_count" required type="number" />
                <select className="luminous-focus h-10 min-w-0 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface)] px-3.5 text-sm text-[var(--ls-text)] outline-none" defaultValue="high" name="highest_severity">
                  <option value="none">No findings</option><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option>
                </select>
              </div>
            </label>
          </div>
          <button className="luminous-focus mt-5 flex w-full items-center justify-center gap-2 rounded-[10px] border border-[var(--ls-line-strong)] bg-[var(--ls-surface-muted)] px-4 py-2.5 text-sm font-semibold text-[var(--ls-text)] transition hover:border-[var(--ls-accent)] hover:bg-[var(--ls-accent-soft)] disabled:cursor-not-allowed disabled:opacity-35" disabled={!enabled || busy !== undefined} type="submit">
            {busy === "route-preview" ? <LoaderCircle className="size-4 animate-spin" /> : <Route className="size-4" />}
            Preview routing
          </button>
        </form></SectionDisclosure>
        ) : null}
      </div>

      {view === "destinations" ? (
      <div className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <div className="flex items-center justify-between gap-4">
          <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Destinations</h2><HelpHint label="Destinations">Pause a robot without deleting its routes or delivery evidence.</HelpHint></div></div>
          <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{destinations.length} targets</span>
        </div>
        <div className="mt-5 grid gap-2 md:grid-cols-2 xl:grid-cols-3">
          {destinations.length === 0 ? <p className="col-span-full rounded-[12px] border border-dashed border-[var(--ls-line-strong)] px-4 py-7 text-center text-sm text-[var(--ls-text-tertiary)]">No notification destination configured yet.</p> : destinations.map((destination) => {
            const presentation = providerPresentation[destination.provider];
            const Icon = presentation.icon;
            return (
              <div className="flex items-center gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3" key={destination.id}>
                <span className="grid size-9 place-items-center rounded-[9px] bg-[var(--ls-surface)]"><Icon className={cn("size-4", presentation.tone)} /></span>
                <div className="min-w-0 flex-1"><p className="truncate text-sm font-medium text-[var(--ls-text)]">{destination.name}</p><p className="mt-0.5 text-xs text-[var(--ls-text-tertiary)]">{presentation.label} · revision {destination.revision}</p></div>
                <div className="flex items-center gap-1">
                  <button aria-label={`Send test to ${destination.name}`} className="luminous-focus grid size-8 place-items-center rounded-[8px] text-[var(--ls-accent)] transition hover:bg-[var(--ls-accent-soft)] disabled:cursor-not-allowed disabled:opacity-40" disabled={!enabled || !destination.enabled || Boolean(busy)} onClick={() => sendDestinationTest(destination)} title="Send test" type="button">
                    {busy === `test:${destination.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
                  </button>
                  <button aria-label={destination.enabled ? "Pause destination" : "Enable destination"} className={cn("luminous-focus grid size-8 place-items-center rounded-[8px] transition", destination.enabled ? "bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] text-[var(--ls-success)] hover:bg-[color:color-mix(in_srgb,var(--ls-success)_20%,transparent)]" : "bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)] hover:text-[var(--ls-text-secondary)]")} disabled={!enabled || Boolean(busy)} onClick={() => toggleDestination(destination)} type="button">
                    {busy === `destination:${destination.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <Power className="size-3.5" />}
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      </div>
      ) : null}

      {view === "routing" ? (
      <div className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <div className="flex items-center justify-between gap-4">
          <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Active routing</h2><HelpHint label="Active routing">Lower priority runs first. Terminal events are queued and delivered independently from the review gate.</HelpHint></div></div>
          <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">{routes.length} routes</span>
        </div>
        {routePreview ? (
          <div className="mt-5 overflow-hidden rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--ls-line)] px-4 py-3">
              <div><p className="text-sm font-medium text-[var(--ls-text)]">Preview result</p><p className="mt-0.5 text-xs text-[var(--ls-text-secondary)]">First matching route wins per destination, using the explicit route priority below.</p></div>
              <div className="flex items-center gap-1.5 text-[11px] font-medium">
                <span className="rounded-full bg-[color:color-mix(in_srgb,var(--ls-success)_14%,transparent)] px-2 py-1 text-[var(--ls-success)]">{routePreviewCounts.selected} selected</span>
                <span className="rounded-full bg-[var(--ls-surface)] px-2 py-1 text-[var(--ls-text-secondary)]">{routePreviewCounts.filtered} filtered</span>
                <span className="rounded-full bg-[var(--ls-accent-soft)] px-2 py-1 text-[var(--ls-accent)]">{routePreviewCounts.deduplicated} deduped</span>
              </div>
            </div>
            {routePreview.matches.length === 0 ? <p className="px-4 py-5 text-sm text-[var(--ls-text-tertiary)]">No saved routes to evaluate.</p> : routePreview.matches.map((match) => (
              <div className="grid gap-2 border-b border-[var(--ls-line)] px-4 py-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center" key={match.route_id}>
                <div className="min-w-0"><p className="truncate text-sm text-[var(--ls-text)]">{match.destination_name}</p><p className="mt-0.5 text-xs text-[var(--ls-text-secondary)]">{match.reason}</p></div>
                <span className={cn("w-fit rounded-full px-2 py-1 text-[11px] font-medium", match.disposition === "selected" ? "bg-[color:color-mix(in_srgb,var(--ls-success)_14%,transparent)] text-[var(--ls-success)]" : match.disposition === "deduplicated" ? "bg-[var(--ls-accent-soft)] text-[var(--ls-accent)]" : "bg-[var(--ls-surface)] text-[var(--ls-text-tertiary)]")}>{match.disposition}</span>
              </div>
            ))}
          </div>
        ) : null}
        <div className="mt-5 space-y-2">
          {routes.length === 0 ? <p className="rounded-[12px] border border-dashed border-[var(--ls-line-strong)] px-4 py-7 text-center text-sm text-[var(--ls-text-tertiary)]">No notification route configured yet.</p> : routes.map((route, index) => (
            <div className="grid gap-3 rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)] px-4 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-center" key={route.id}>
              <div className="flex items-center gap-2 text-sm text-[var(--ls-text)]"><span className="grid size-5 shrink-0 place-items-center rounded-full bg-[var(--ls-surface)] text-[10px] font-semibold text-[var(--ls-text-secondary)]" title="Route priority">{route.priority}</span><GitBranch className="size-4 text-[var(--ls-text-tertiary)]" /><span className="font-mono text-xs">{route.repository_glob}</span></div>
              <div className="text-xs text-[var(--ls-text-secondary)]">{destinationNames.get(route.destination_id) ?? "Unknown destination"} · {route.branch_glob} · {route.event_types.length} events · {route.min_severity}+</div>
              <div className="flex items-center justify-end gap-1">
                <button aria-label={`Move ${route.repository_glob} earlier`} className="luminous-focus grid size-7 place-items-center rounded-[8px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface)] hover:text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-35" disabled={!enabled || Boolean(busy) || index === 0} onClick={() => moveRoute(route.id, -1)} title="Move earlier" type="button"><ArrowUp className="size-3.5" /></button>
                <button aria-label={`Move ${route.repository_glob} later`} className="luminous-focus grid size-7 place-items-center rounded-[8px] text-[var(--ls-text-secondary)] transition hover:bg-[var(--ls-surface)] hover:text-[var(--ls-text)] disabled:cursor-not-allowed disabled:opacity-35" disabled={!enabled || Boolean(busy) || index === routes.length - 1} onClick={() => moveRoute(route.id, 1)} title="Move later" type="button"><ArrowDown className="size-3.5" /></button>
                <button className={cn("luminous-focus flex items-center gap-1.5 rounded-[8px] px-2 py-1 text-xs transition", route.enabled ? "text-[var(--ls-success)] hover:bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)]" : "text-[var(--ls-text-tertiary)] hover:bg-[var(--ls-surface)] hover:text-[var(--ls-text-secondary)]")} disabled={!enabled || Boolean(busy)} onClick={() => toggleRoute(route)} type="button">
                  {busy === `route:${route.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}{route.enabled ? "Active" : "Paused"}
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>
      ) : null}

      {view === "deliveries" ? (
      <div className="rounded-[20px] border border-[var(--ls-line)] bg-[var(--ls-surface)] p-5 shadow-[var(--ls-shadow-control)] sm:p-6">
        <div className="flex items-center justify-between gap-4">
          <div><div className="flex min-w-0 items-center gap-2"><h2 className="text-sm font-semibold text-[var(--ls-text)]">Delivery history</h2><HelpHint label="Delivery history">Provider responses are reduced to safe status metadata; secret-bearing bodies are never retained.</HelpHint></div></div>
          <span className="rounded-full bg-[var(--ls-surface-muted)] px-2.5 py-1 text-xs text-[var(--ls-text-secondary)]">Latest {deliveries.length}</span>
        </div>
        <div className="mt-5 overflow-hidden rounded-[12px] border border-[var(--ls-line)] bg-[var(--ls-surface-muted)]">
          {deliveries.length === 0 ? <p className="px-4 py-7 text-center text-sm text-[var(--ls-text-tertiary)]">No delivery attempts yet.</p> : deliveries.map((delivery) => (
            <div className="grid gap-2 border-b border-[var(--ls-line)] px-4 py-3 last:border-b-0 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto_auto] md:items-center" key={delivery.id}>
              <div className="min-w-0"><p className="truncate text-sm text-[var(--ls-text)]">{delivery.destination_name}</p><p className="mt-0.5 truncate font-mono text-[11px] text-[var(--ls-text-tertiary)]">{delivery.event_type === "notification.destination.test" ? "Test notification" : delivery.event_type}</p></div>
              <div className="text-xs text-[var(--ls-text-secondary)]">{deliveryDateFormatter.format(new Date(delivery.created_at))} UTC · attempt {delivery.attempt}</div>
              <span className={cn("w-fit rounded-full px-2 py-1 text-[11px] font-medium", delivery.state === "delivered" ? "bg-[color:color-mix(in_srgb,var(--ls-success)_12%,transparent)] text-[var(--ls-success)]" : delivery.state === "failed" ? "bg-red-500/10 text-[var(--ls-critical-text)]" : "bg-[color:color-mix(in_srgb,var(--ls-warning)_12%,transparent)] text-[var(--ls-warning)]")}>{delivery.state}</span>
              {delivery.state === "failed" && delivery.event_type !== "notification.destination.test" ? <button className="luminous-focus flex items-center gap-1.5 rounded-[8px] px-2 py-1 text-xs text-[var(--ls-accent)] transition hover:bg-[var(--ls-accent-soft)]" disabled={!enabled || Boolean(busy)} onClick={() => retryDelivery(delivery)} type="button">{busy === `delivery:${delivery.id}` ? <LoaderCircle className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}Retry</button> : <span />}
            </div>
          ))}
        </div>
      </div>
      ) : null}
      </fieldset>
    </div>
  );
}
