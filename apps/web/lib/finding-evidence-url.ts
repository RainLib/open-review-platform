const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const WORKSPACE_SLUG = /^[a-z0-9][a-z0-9-]{1,62}$/;

export function findingEvidenceURL(org: string, runID?: string, findingID?: string) {
  if (!WORKSPACE_SLUG.test(org) || !runID || !findingID || !UUID.test(runID) || !UUID.test(findingID)) {
    return undefined;
  }
  return `/${encodeURIComponent(org)}/reviews/${encodeURIComponent(runID)}?tab=findings#finding-${encodeURIComponent(findingID)}`;
}
