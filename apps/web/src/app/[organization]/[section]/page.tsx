import { notFound } from "next/navigation";

import { isWorkspaceSection, SectionPlaceholder } from "@/components/workspace/section-placeholder";

type PageProps = { params: Promise<{ organization: string; section: string }> };

export default async function WorkspaceSectionPage({ params }: PageProps) {
  const { organization, section } = await params;
  if (!isWorkspaceSection(section)) notFound();
  return <SectionPlaceholder organization={organization} section={section} />;
}
