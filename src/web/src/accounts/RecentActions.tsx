import { Timestamp } from "@/components/kraken/timestamp";
import { useEffect, useState } from "react";
import { ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { actionLabel } from "../audit/actions";
import type { SectionProps } from "../sections/sectionProps";

type Row = { id: string; action: string; detail?: string; timestamp: string };

// RecentActions shows the user's last audit rows (GET /audit?actor=) with a
// jump to the full audit.
export function RecentActions({
  username,
  request,
  onOpenAudit,
}: {
  username: string;
  request: SectionProps["request"];
  onOpenAudit: () => void;
}) {
  const [items, setItems] = useState<Row[] | null>(null);
  useEffect(() => {
    let live = true;
    setItems(null);
    request<{ items: Row[] }>(
      `/audit?actor=${encodeURIComponent(username)}&limit=5`,
    )
      .then((r) => live && setItems(r.items ?? []))
      .catch(() => live && setItems([]));
    return () => {
      live = false;
    };
  }, [request, username]);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Последние действия</CardTitle>
        <CardAction>
          <Button size="xs" variant="ghost" onClick={onOpenAudit}>
            Весь аудит <ArrowRight aria-hidden />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col">
        {items?.length === 0 && (
          <span className="text-sm text-muted-foreground">Действий нет</span>
        )}
        {(items ?? []).map((a) => (
          <div
            key={a.id}
            className="flex items-center gap-3 border-b py-2 last:border-b-0"
          >
            <span className="min-w-0 grow truncate">
              {a.detail || actionLabel(a.action)}
            </span>
            <span className="shrink-0 text-sm text-muted-foreground tabular-nums">
              <Timestamp value={a.timestamp} compact />
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
