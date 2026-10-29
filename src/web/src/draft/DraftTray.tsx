import { CheckCircle2, Eye } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Status } from "../model";

// Bottom tray: shows the pending-changes count when /status.has_unapplied_changes
// is true, and the "review" CTA. Empty state surfaces the active revision.
export function DraftTray({
  status,
  onReview,
  onDiscard,
  onOpenAudit,
}: {
  status: Status | null;
  onReview: () => void;
  onDiscard: () => void;
  onOpenAudit: () => void;
}) {
  const hasChanges = status?.has_unapplied_changes === true;
  const rev = status?.applied_revision ?? null;
  return (
    <div className="draft-tray">
      {hasChanges ? (
        <>
          <span className="pill pill-mod">Есть неприменённые изменения</span>
          <span className="muted-text small hide-md">
            Трафик не изменится, пока изменения не применены
          </span>
          <span className="grow" />
          <Button size="sm" variant="ghost" onClick={onDiscard}>
            Сбросить черновик
          </Button>
          <Button size="sm" variant="default" onClick={onReview}>
            <Eye aria-hidden /> Проверить и применить
          </Button>
        </>
      ) : (
        <>
          <span className="pill pill-ok">
            <CheckCircle2 aria-hidden />
            Черновик пуст
          </span>
          <span className="muted-text small">
            {rev ? `Работает ревизия ${short(rev)}` : "Конфигурация активна"}
          </span>
          <span className="grow" />
          <Button size="sm" variant="ghost" onClick={onOpenAudit}>
            История применений
          </Button>
        </>
      )}
    </div>
  );
}

function short(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}
