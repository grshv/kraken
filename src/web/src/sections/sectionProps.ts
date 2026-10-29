import type { Request } from "../api";
import type { Draft, Session, Snapshot } from "../model";
import type { NavId } from "../shell/nav";
import type { ConfirmOptions } from "../shell/ConfirmDialog";

// Result of a draft commit: the server error code (stable) when not ok, so the
// screen can surface source_outside_scope / source_overlap /
// policy_shared_out_of_scope / username_taken at the relevant field and toast
// on revision_conflict.
export type CommitResult = {
  ok: boolean;
  code?: string;
  message?: string;
};

// Props every section screen receives from the shell. The shell owns the draft
// and active snapshot; sections compute the next snapshot with pure helpers and
// hand it back via onCommit. onDiscard reset the draft to the active snapshot.
export type SectionProps = {
  request: Request;
  draft: Draft | null;
  active: Snapshot | null;
  session: Session;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onDiscard: () => Promise<CommitResult>;
  onToast: (message: string) => void;
  onNavigate: (id: NavId) => void;
  onConfirm: (options: ConfirmOptions) => void;
};

// Manager scope extracted from the session for source-network checks.
export function managerScope(session: Session): {
  networks: string[];
  clientIDs: string[];
} | null {
  // The server exposes scope only for managers; admin sees everything.
  if (session.role !== "manager") return null;
  const s = session.scope;
  return s
    ? { networks: s.networks ?? [], clientIDs: s.client_ids ?? [] }
    : { networks: [], clientIDs: [] };
}
