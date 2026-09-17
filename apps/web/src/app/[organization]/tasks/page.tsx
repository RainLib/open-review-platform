import { TaskList } from "@/components/workspace/task-list";

type PageProps = { params: Promise<{ organization: string }> };

export default async function TasksPage({ params }: PageProps) {
  const { organization } = await params;
  return <TaskList organization={organization} />;
}
