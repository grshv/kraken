import { Eye } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Status } from "../model";

// Bottom tray: appears only when /status.has_unapplied_changes is true and
// offers discard and the "review" CTA. With an empty draft it is not rendered.
export function DraftTray({
  status,
  onReview,
  onDiscard,
}: {
  status: Status | null;
  onReview: () => void;
  onDiscard: () => void;
}) {
  if (status?.has_unapplied_changes !== true) return null;
  return (
    <div className="draft-tray">
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
    </div>
  );
}
