import { OperatePageHeader } from "@/components/console/operate-page-header";
import { NotificationManager } from "@/components/console/notification-manager";
import { getNotificationData } from "@/lib/control-api";

type NotificationTab = "destinations" | "routing" | "deliveries";

export default async function NotificationsPage({ params, searchParams }: { params: Promise<{ org: string }>; searchParams: Promise<{ tab?: string }> }) {
  const [{ org }, query] = await Promise.all([params, searchParams]);
  const data = await getNotificationData(org);
  const tab: NotificationTab = query.tab === "routing" || query.tab === "deliveries" ? query.tab : "destinations";
  const presentation = tab === "routing" ? { active: "routing" as const, eyebrow: "Delivery policy", title: "Event routing", description: "Route exact terminal events by repository, branch, and severity without coupling notification delivery to the review gate." } : tab === "deliveries" ? { active: "deliveries" as const, eyebrow: "Delivery evidence", title: "Delivery logs", description: "Inspect asynchronous attempts, safe provider status, retry eligibility, and idempotent delivery history." } : { active: "destinations" as const, eyebrow: "Delivery control", title: "Notification destinations", description: "Manage DingTalk, Feishu, Slack, and generic webhook targets while keeping credentials in deployment-owned secret references." };
  return <div className="space-y-7"><OperatePageHeader {...presentation} detail={data.detail} org={org} source={data.source} /><NotificationManager deliveries={data.deliveries} destinations={data.destinations} detail={data.detail} enabled={data.source === "live"} org={org} routes={data.routes} source={data.source} view={tab} /></div>;
}
