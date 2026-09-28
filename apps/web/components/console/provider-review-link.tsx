import { ExternalLink } from "lucide-react";

import { ProviderMark } from "@/components/providers/provider-icons";
import type { ReviewRun } from "@/lib/control-api";
import { providerReviewTarget } from "@/lib/provider-review-url";

export function ProviderReviewLink({
  className,
  run,
}: {
  className?: string;
  run: Pick<ReviewRun, "provider" | "api_base_url" | "repository" | "review_number">;
}) {
  const target = providerReviewTarget(run);
  if (!target) return null;

  return (
    <a
      className={className}
      href={target.url}
      rel="noreferrer"
      target="_blank"
      title={`Open in ${target.label}`}
    >
      <ProviderMark className="size-4" provider={run.provider} />
      <span>{target.label}</span>
      <ExternalLink className="size-3.5" />
    </a>
  );
}
