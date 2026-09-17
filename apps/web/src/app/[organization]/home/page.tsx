import { HomeDashboard } from "@/components/workspace/home-dashboard";

type PageProps = { params: Promise<{ organization: string }> };

export default async function HomePage({ params }: PageProps) {
  const { organization } = await params;
  return <HomeDashboard organization={organization} />;
}
