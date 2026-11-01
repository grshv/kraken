import {Disclosure} from '@/components/ui/disclosure';
import {Timestamp} from '@/components/kraken/timestamp';
import { useCallback, useEffect, useState } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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
  // The current session first, then the most recently active.
  const sorted = [...(items ?? [])].sort(
    (a, b) =>
      Number(b.current) - Number(a.current) ||
      Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at),
  );
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Сессии</CardTitle>
          <Disclosure className="mt-1 text-sm text-muted-foreground" label="Срок сессии">
            <p className="mt-2">12 ч или 30 мин бездействия</p>
          </Disclosure>
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
      <CardContent>
        {items !== null && items.length === 0 && (
          <span className="text-sm text-muted-foreground">
            Активных сессий нет
          </span>
        )}
        {items !== null && items.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Устройство</TableHead>
                <TableHead>Адрес</TableHead>
                <TableHead>Начало</TableHead>
                <TableHead>Активность</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.map((s) => (
                <TableRow key={s.id}>
                  <TableCell title={s.user_agent ?? undefined}>
                    {deviceLabel(s.user_agent)}
                    {s.current && (
                      <Badge variant="secondary" className="ml-1.5">
                        эта
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="font-mono">{s.remote ?? "—"}</TableCell>
                  <TableCell className="text-muted-foreground tabular-nums">
                    <Timestamp value={s.created_at} seconds={false} compact/>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    <Timestamp value={s.last_seen_at} mode="relative"/>
                  </TableCell>
                  <TableCell className="text-right">
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
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

