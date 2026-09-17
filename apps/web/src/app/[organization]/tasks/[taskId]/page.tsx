import { TaskCanvas } from "@/components/workspace/task-canvas";

type PageProps = { params: Promise<{ organization: string; taskId: string }> };

export default async function TaskPage({ params }: PageProps) {
  const { organization, taskId } = await params;
  return <TaskCanvas organization={organization} taskId={taskId} />;
}
