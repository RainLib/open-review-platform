import { redirect } from "next/navigation";

// Keep the enterprise area addressable at its stable root. Navigation already
// selects Members first, and a server redirect avoids exposing a dead 404 to
// bookmarks, command-palette links, or an older workspace URL.
export default async function SettingsPage({
  params,
}: {
  params: Promise<{ org: string }>;
}) {
  const { org } = await params;
  redirect(`/${org}/settings/members`);
}
