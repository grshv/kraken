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
    <div className="flex shrink-0 items-center gap-2.5 border-t bg-card px-4 py-[9px]">
      <span className="inline-flex items-center gap-2 font-medium text-mod-fg">
        <span className="size-2 rounded-full bg-mod-fg" aria-hidden />
        Есть неприменённые изменения
      </span>
      <span className="text-sm text-muted-foreground">
        Трафик не изменится, пока изменения не применены
      </span>
      <span className="grow" />
      <Button size="sm" variant="ghost" onClick={onDiscard}>
        Сбросить черновик
      </Button>
      <Button size="sm" onClick={onReview}>
        Проверить и применить
      </Button>
    </div>
  );
}
