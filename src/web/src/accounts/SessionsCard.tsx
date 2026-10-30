import { useCallback, useEffect, useState } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { ago } from "../model";
import type { SectionProps } from "../sections/sectionProps";
import { deviceLabel } from "./device";

type PanelSession = {
  id: string;
  created_at: string | null;
  last_seen_at: string;
  expires_at: string;
  remote: string | null;
  user_agent: string | null;
  current: boolean;
};

// SessionsCard lists live panel sessions of one user (GET
// /users/{name}/sessions) with "Завершить" per session and "Завершить
// другие" / "Завершить все" (the caller's own session is always kept).
export function SessionsCard({
  username,
  request,
  onToast,
}: {
  username: string;
  request: SectionProps["request"];
  onToast: (m: string) => void;
}) {
  const [items, setItems] = useState<PanelSession[] | null>(null);
  const base = `/users/${encodeURIComponent(username)}/sessions`;
  const load = useCallback(
    () =>
      request<{ items: PanelSession[] }>(base)
        .then((r) => setItems(r.items))
        .catch(() => setItems([])),
    [request, base],
  );
  useEffect(() => {
    setItems(null);
    void load();
  }, [load]);
  const revoke = async (path: string, message: string) => {
    try {
      await request(path, "DELETE");
      onToast(message);
    } catch (e) {
      onToast((e as Error).message);
    }
    await load();
  };
  const others = (items ?? []).filter((s) => !s.current);
  const hasCurrent = (items ?? []).some((s) => s.current);
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Сессии</CardTitle>
          <CardDescription>12 ч или 30 мин бездействия</CardDescription>
        </div>
        {others.length > 0 && (
          <CardAction>
            <Button
              size="xs"
              variant="outline"
              onClick={() =>
                void revoke(
                  base,
                  hasCurrent ? "Другие сессии завершены" : "Сессии завершены",
                )
              }
            >
              {hasCurrent ? "Завершить другие" : "Завершить все"}
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col">
        {items !== null && items.length === 0 && (
          <span className="text-sm text-muted-foreground">
            Активных сессий нет
          </span>
        )}
        {(items ?? []).map((s) => (
          <div
            key={s.id}
            className="flex items-center gap-2 border-b py-1.5 last:border-b-0"
          >
            <div className="min-w-0 grow">
              <div className="truncate" title={s.user_agent ?? undefined}>
                {deviceLabel(s.user_agent)}
                {s.current && (
                  <Badge variant="secondary" className="ml-1.5">
                    эта
                  </Badge>
                )}
              </div>
              <div className="truncate text-sm text-muted-foreground">
                {s.remote && <span className="font-mono">{s.remote}</span>}
                {s.created_at &&
                  ` · с ${new Date(s.created_at).toLocaleString("ru-RU", {
                    day: "2-digit",
                    month: "2-digit",
                    hour: "2-digit",
                    minute: "2-digit",
                  })}`}
                {` · ${seen(s.last_seen_at)}`}
              </div>
            </div>
            {!s.current && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() =>
                  void revoke(`${base}/${s.id}`, "Сессия завершена")
                }
              >
                <X aria-hidden /> Завершить
              </Button>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function seen(iso: string): string {
  const a = ago(iso);
  return a === "сейчас" ? "активна сейчас" : `активна ${a} назад`;
}
