export type RunState =
  | "acknowledged"
  | "admitted"
  | "preparing"
  | "analyzing"
  | "normalizing"
  | "publishing"
  | "completed"
  | "failed"
  | "cancelled"
  | "superseded"
  | "needs_attention";

export type ReviewRun = {
  id: string;
  revision: number;
  state: RunState;
  trigger_kind: string;
  head_sha: string;
  base_sha: string;
  failure_code?: string;
  failure_message?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  provider: "github" | "gitlab";
  repository: string;
  review_number: number;
};

export type RuleSet = {
  id: string;
  name: string;
  description: string;
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type ProviderInstallation = {
  id: string;
  provider: "github" | "gitlab";
  external_id: string;
  repository_scope: string;
  api_base_url: string;
  active: boolean;
};

export type RuleSnapshot = {
  id: string;
  sha256: string;
  compiler_version: string;
  engine: string;
  sources: Array<{
    rule_version_id: string;
    rule_set_id: string;
    version: number;
    precedence: number;
  }>;
  created_at: string;
};

export type RunEvent = {
  id: string;
  run_id: string;
  revision: number;
  event_type: string;
  actor_kind: string;
  actor_subject?: string;
  payload: Record<string, unknown>;
  created_at: string;
};

export type DataSource = "live" | "demo" | "unconfigured" | "unavailable";

export type ConsoleData = {
  source: DataSource;
  runs: ReviewRun[];
  ruleSets: RuleSet[];
  installations: ProviderInstallation[];
  detail?: string;
};

export type RunDetailData = ConsoleData & {
  run?: ReviewRun;
  ruleSnapshot?: RuleSnapshot;
};

const demoRuns: ReviewRun[] = [
  {
    id: "demo-42a1",
    revision: 7,
    state: "analyzing",
    trigger_kind: "pull_request",
    head_sha: "bd91254e6af2",
    base_sha: "c21d09db9c74",
    created_at: "2026-09-18T02:04:00Z",
    started_at: "2026-09-18T02:04:14Z",
    provider: "github",
    repository: "RainLib/open-review-platform",
    review_number: 42,
  },
  {
    id: "demo-31f9",
    revision: 4,
    state: "needs_attention",
    trigger_kind: "comment",
    head_sha: "4e17c8f9a122",
    base_sha: "2ad4adf1c8b0",
    created_at: "2026-09-18T01:36:00Z",
    started_at: "2026-09-18T01:36:09Z",
    finished_at: "2026-09-18T01:39:35Z",
    provider: "github",
    repository: "RainLib/open-review-platform",
    review_number: 39,
  },
  {
    id: "demo-080c",
    revision: 3,
    state: "completed",
    trigger_kind: "pull_request",
    head_sha: "c0f4e15b19a7",
    base_sha: "4a76d9ba0f11",
    created_at: "2026-09-17T14:22:00Z",
    started_at: "2026-09-17T14:22:07Z",
    finished_at: "2026-09-17T14:25:17Z",
    provider: "gitlab",
    repository: "platform/agent-harness",
    review_number: 118,
  },
];

const demoRuleSets: RuleSet[] = [
  {
    id: "rule-auth-boundary",
    name: "Authentication boundary",
    description:
      "Require an explicit trust-boundary review when identity or token handling changes.",
    created_by: "security@acme.example",
    created_at: "2026-09-04T08:00:00Z",
    updated_at: "2026-09-17T10:31:00Z",
  },
  {
    id: "rule-release-evidence",
    name: "Release evidence",
    description:
      "Ask for verification and rollback evidence when production paths are changed.",
    created_by: "platform@acme.example",
    created_at: "2026-08-29T08:00:00Z",
    updated_at: "2026-09-15T07:19:00Z",
  },
];

const demoInstallations: ProviderInstallation[] = [
  {
    id: "installation-demo-1",
    provider: "github",
    external_id: "123456",
    repository_scope: "RainLib/*",
    api_base_url: "https://api.github.com",
    active: true,
  },
];

const demoEvents: Record<string, RunEvent[]> = {
  "demo-42a1": [
    {
      id: "event-42-1",
      run_id: "demo-42a1",
      revision: 1,
      event_type: "run.acknowledged",
      actor_kind: "provider",
      payload: { trigger: "pull_request" },
      created_at: "2026-09-18T02:04:00Z",
    },
    {
      id: "event-42-2",
      run_id: "demo-42a1",
      revision: 2,
      event_type: "run.admitted",
      actor_kind: "system",
      payload: { snapshot: "resolved" },
      created_at: "2026-09-18T02:04:03Z",
    },
    {
      id: "event-42-3",
      run_id: "demo-42a1",
      revision: 4,
      event_type: "run.preparing",
      actor_kind: "worker",
      payload: { checkout: "pinned" },
      created_at: "2026-09-18T02:04:14Z",
    },
    {
      id: "event-42-4",
      run_id: "demo-42a1",
      revision: 7,
      event_type: "run.analyzing",
      actor_kind: "worker",
      payload: { engine: "open-code-review" },
      created_at: "2026-09-18T02:05:10Z",
    },
  ],
};

function configured() {
  return (
    process.env.NODE_ENV !== "production" &&
    Boolean(
      process.env.CONTROL_API_URL &&
        process.env.CONTROL_API_DEVELOPMENT_SUBJECT,
    )
  );
}

async function request<T>(path: string): Promise<T> {
  const response = await fetch(`${process.env.CONTROL_API_URL}${path}`, {
    cache: "no-store",
    headers: {
      "X-Development-Subject":
        process.env.CONTROL_API_DEVELOPMENT_SUBJECT ?? "",
    },
  });

  if (!response.ok) {
    throw new Error(`Control plane returned ${response.status}`);
  }

  return response.json() as Promise<T>;
}

export async function getConsoleData(org: string): Promise<ConsoleData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      runs: demoRuns,
      ruleSets: demoRuleSets,
      installations: demoInstallations,
    };
  }

  if (!configured()) {
    return {
      source: "unconfigured",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        "Set CONTROL_API_URL and CONTROL_API_DEVELOPMENT_SUBJECT for a local development connection.",
    };
  }

  try {
    const [runs, ruleSets, installations] = await Promise.all([
      request<{ runs: ReviewRun[] }>(
        `/v1/tenants/${encodeURIComponent(org)}/runs?limit=25`,
      ),
      request<{ rule_sets: RuleSet[] }>(
        `/v1/tenants/${encodeURIComponent(org)}/rule-sets?limit=25`,
      ),
      request<{ installations: ProviderInstallation[] }>(
        `/v1/tenants/${encodeURIComponent(org)}/installations?limit=25`,
      ),
    ]);

    return {
      source: "live",
      runs: runs.runs,
      ruleSets: ruleSets.rule_sets,
      installations: installations.installations,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        error instanceof Error
          ? error.message
          : "The control plane could not be reached.",
    };
  }
}

async function optionalRequest<T>(path: string): Promise<T | undefined> {
  const response = await fetch(`${process.env.CONTROL_API_URL}${path}`, {
    cache: "no-store",
    headers: {
      "X-Development-Subject":
        process.env.CONTROL_API_DEVELOPMENT_SUBJECT ?? "",
    },
  });
  if (response.status === 404) return undefined;
  if (!response.ok)
    throw new Error(`Control plane returned ${response.status}`);
  return response.json() as Promise<T>;
}

export async function getRunDetail(
  org: string,
  runID: string,
): Promise<RunDetailData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const run = demoRuns.find((candidate) => candidate.id === runID);
    return {
      source: "demo",
      runs: demoRuns,
      ruleSets: demoRuleSets,
      installations: demoInstallations,
      run,
      ruleSnapshot: run
        ? {
            id: "snapshot-demo-042",
            sha256:
              "8c59b208d0ece6da835e7700fa9a3bb9012e49105a14d2e34f7ee870768a10cf",
            compiler_version: "rules-v1",
            engine: "open-code-review",
            sources: [
              {
                rule_version_id: "version-demo-1",
                rule_set_id: "rule-auth-boundary",
                version: 4,
                precedence: 100,
              },
            ],
            created_at: "2026-09-18T02:04:02Z",
          }
        : undefined,
    };
  }

  if (!configured()) {
    return {
      source: "unconfigured",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        "Set CONTROL_API_URL and CONTROL_API_DEVELOPMENT_SUBJECT for a local development connection.",
    };
  }

  try {
    const run = await request<ReviewRun>(
      `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}`,
    );
    const ruleSnapshot = await optionalRequest<RuleSnapshot>(
      `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/rule-snapshot`,
    );
    return {
      source: "live",
      runs: [],
      ruleSets: [],
      installations: [],
      run,
      ruleSnapshot,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        error instanceof Error
          ? error.message
          : "The control plane could not be reached.",
    };
  }
}

export function getDemoRunEvents(runID: string) {
  return demoEvents[runID] ?? [];
}
