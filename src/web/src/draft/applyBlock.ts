import type { Draft } from "../model";

// applyBlockedReason explains, before "Применить", why a manager cannot apply
// the draft: the server reports foreign changes without naming them.
export function applyBlockedReason(draft: Draft | null): string | undefined {
  if (!draft?.foreign_changes) return undefined;
  return "В черновике есть изменения вне вашей области. Применить черновик может администратор; ваши изменения можно отменить.";
}
