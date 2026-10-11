import type { AgentTaskDetail } from "./control-api";

export type CampaignRepository = { installation_id: string; repository: string };
export type CampaignScan = { base_ref: string; base_sha: string; complete: boolean; files_scanned: number; files_excluded: number; matches: number; files: { path: string; sha256: string; matches: number; bytes: number }[] };
export type Campaign = { id: string; state: string; revision: number; request_sha256: string; requested_by: string; created_at: string; input: { title: string; mode: string; requirements: string; acceptance_criteria: string[]; paths: string[]; concurrency: number } };
export type CampaignTarget = { id: string; repository: string; state: string; scan: CampaignScan; task_id?: string; detail?: AgentTaskDetail; error_code?: string; error_message?: string; scan_attempts: number };
export type CampaignDetail = { campaign: Campaign; targets: CampaignTarget[]; summary: { total: number; counts: Record<string, number>; closed: boolean; conclusion: string } };
