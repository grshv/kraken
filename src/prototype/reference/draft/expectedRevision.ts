// Pure chooser for the expected_applied_revision value sent to validate/apply.
// The server compares this against the currently applied revision: a mismatch
// returns 409 revision_conflict. Rules:
// - preview value wins when present (admin reads it from
//   /revisions/{id}/preview);
// - otherwise fall back to GET /status .applied_revision, available to every
//   role including manager (who cannot fetch preview, 403 admin_required);
// - null is only valid when there is no applied revision yet (first-ever
//   apply), i.e. when status.applied_revision itself is null.
export type PreviewExpected = {
  expected_applied_revision?: string | null;
} | null;

export function pickExpectedRevision(
  preview: PreviewExpected,
  statusApplied: string | null,
): string | null {
  const fromPreview = preview?.expected_applied_revision;
  if (fromPreview) {
    return fromPreview;
  }
  return statusApplied ?? null;
}
