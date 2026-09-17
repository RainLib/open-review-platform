export type TaskState = "running" | "queued" | "completed" | "attention";

export type ReviewTask = {
  id: string;
  title: string;
  repository: string;
  pullRequest: string;
  state: TaskState;
  mode: "Standard" | "Deep" | "Security";
  progress?: number;
  summary: string;
  elapsed: string;
  estimatedCost: string;
  people: string[];
};

export const reviewTasks: ReviewTask[] = [
  {
    id: "ORP-1842",
    title: "Review payment retry logic",
    repository: "payments-api",
    pullRequest: "PR #8421",
    state: "running",
    mode: "Deep",
    progress: 68,
    summary: "Tracing retry paths across payment boundaries",
    elapsed: "2m 14s",
    estimatedCost: "$0.12",
    people: ["MK", "AP", "LD"],
  },
  {
    id: "ORP-1843",
    title: "Checkout resilience review",
    repository: "checkout-web",
    pullRequest: "PR #184",
    state: "queued",
    mode: "Deep",
    summary: "Security + reliability policy is resolving",
    elapsed: "~8 min wait",
    estimatedCost: "$0.28 est.",
    people: ["JR", "YC"],
  },
  {
    id: "ORP-1836",
    title: "Identity session boundaries",
    repository: "identity",
    pullRequest: "PR #607",
    state: "completed",
    mode: "Security",
    summary: "6 findings · 2 resolved by the author",
    elapsed: "4 min",
    estimatedCost: "$0.19",
    people: ["ML", "FD"],
  },
];

export const activeTask = reviewTasks[0];
