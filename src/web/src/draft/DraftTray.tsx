import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { pluralRu, type Status } from "../model";

// Bottom tray: appears only when /status.has_unapplied_changes is true and
// offers discard and the "review" CTA. With an empty draft it is not rendered.
export function DraftTray({
  status,
  changes,
  onReview,
  onDiscard,
}: {
  status: Status | null;
  // Changed objects of the draft (draftChangeCount); null when unknown.
  changes?: number | null;
  onReview: () => void;
  onDiscard: () => void;
}) {
  if (status?.has_unapplied_changes !== true) return null;
  return (
    <div className="flex h-14 shrink-0 items-center gap-3 border-t bg-header px-6">
      <Badge
        variant="warn"
        className="gap-2 rounded-md border border-warn-border bg-warn-bg px-2.5 py-1 text-base"
      >
        <span className="size-1.5 rounded-full bg-current" aria-hidden />
        Черновик конфигурации
      </Badge>
      <span className="text-sm text-muted-foreground">
        {changes
          ? `${pluralRu(changes, "изменение", "изменения", "изменений")} · `
          : ""}
        не применён, трафик пока не изменился
      </span>
      <span className="grow" />
      <Button size="sm" variant="ghost" onClick={onDiscard}>
        Сбросить
      </Button>
      <Button size="sm" onClick={onReview}>
        Проверить и применить
      </Button>
    </div>
  );
}
