import { PolicyStudio } from "@/components/workspace/policy-studio";

type PageProps = { params: Promise<{ organization: string }> };

export default async function RulesPage({ params }: PageProps) {
  const { organization } = await params;
  return <PolicyStudio organization={organization} />;
}
