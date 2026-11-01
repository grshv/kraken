import {Timestamp} from '@/components/kraken/timestamp';
import { useMemo, useState } from "react";
import {
  Ban,
  Check,
  EyeOff,
  MoreHorizontal,
  Plus,
  RotateCcw,
  Search,
} from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Client, Snapshot } from "../model";
import { ago, pluralRu } from "../model";
import type { CommitResult } from "../sections/sectionProps";
import {
  addDomainsToClient,
  blockDomainsForAll,
  clientAllows,
} from "../draft/edits";
import { countNotices, type Notice, type NoticeStatus } from "./notifications";

type Filter = NoticeStatus | "all";
const PAGE = 50;

// DeniedDomains is the "Доступ" tab table of denied domains the client may
// not reach (7 days, computed on the server). It scales to hundreds of rows:
// search, status filter, select-all, bulk allow (into the client's own
// domains) / mark / block for all. Hosts already allowed in the draft stay
// listed until the draft is applied and are marked "в черновике".
export function DeniedDomains({
  client,
  snap,
  items,
  canBlock,
  onMark,
  onCommit,
  onToast,
}: {
  client: Client;
  snap: Snapshot;
  items: Notice[] | null;
  canBlock: boolean;
  onMark: (ids: string[], status: NoticeStatus) => Promise<void>;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
}) {
  const [filter, setFilter] = useState<Filter>("new");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [limit, setLimit] = useState(PAGE);
  const counts = countNotices(items);
  const blocked = snap.settings?.blocked_domains ?? [];
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (items ?? []).filter(
      (n) =>
        (filter === "all" || n.status === filter) &&
        (!q || (n.host ?? "").includes(q)),
    );
  }, [items, filter, query]);
  if (!items || items.length === 0) return null;
  const shown = rows.slice(0, limit);
  const picked = rows.filter((n) => selected.has(n.id));
  const allPicked = shown.length > 0 && shown.every((n) => selected.has(n.id));
  const inDraft = (host: string) =>
    clientAllows(snap, client, host) || blocked.some((b) => b.domain === host);
  const hostsOf = (list: Notice[]) =>
    list.map((n) => n.host).filter((h): h is string => !!h);
  const clear = () => setSelected(new Set());
  const toggle = (id: string, on: boolean) =>
    setSelected((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  const allow = async (list: Notice[]) => {
    const hosts = hostsOf(list);
    const res = await onCommit(addDomainsToClient(snap, client.id, hosts));
    if (!res.ok) return;
    onToast(
      hosts.length === 1
        ? `Домен ${hosts[0]} добавлен клиенту`
        : `${pluralRu(hosts.length, "домен", "домена", "доменов")} добавлено клиенту`,
    );
    clear();
  };
  const block = async (list: Notice[]) => {
    const hosts = hostsOf(list);
    const res = await onCommit(blockDomainsForAll(snap, hosts));
    if (!res.ok) return;
    onToast(
      `${pluralRu(hosts.length, "домен", "домена", "доменов")} запрещено для всех`,
    );
    clear();
  };
  const mark = async (list: Notice[], status: NoticeStatus) => {
    await onMark(
      list.map((n) => n.id),
      status,
    );
    clear();
  };
  const statusBadge = (n: Notice) => {
    if (n.status === "new") return <Badge variant="bad">новый</Badge>;
    const who = n.marked_by ? ` · ${n.marked_by}` : "";
    return (
      <span className="text-sm text-muted-foreground">
        {n.status === "seen" ? "просмотрено" : "игнорируется"}
        {who}
        {n.marked_at&&<>, <Timestamp value={n.marked_at} mode="relative"/></>}
      </span>
    );
  };
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Отказанные домены</CardTitle>
          <CardDescription>
            За 7 дней
          </CardDescription>
        </div>
        <CardAction>
          <ToggleGroup
            type="single"
            variant="seg"
            size="sm"
            spacing={0.5}
            className="rounded-lg bg-muted p-[3px]"
            aria-label="Статус"
            value={filter}
            onValueChange={(v) => {
              if (!v) return;
              setFilter(v as Filter);
              setLimit(PAGE);
              clear();
            }}
          >
            <ToggleGroupItem value="new">Новые {counts.new}</ToggleGroupItem>
            <ToggleGroupItem value="seen">
              Просмотренные {counts.seen}
            </ToggleGroupItem>
            <ToggleGroupItem value="ignored">
              Игнор {counts.ignored}
            </ToggleGroupItem>
            <ToggleGroupItem value="all">Все</ToggleGroupItem>
          </ToggleGroup>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <div className="flex min-h-8 flex-wrap items-center gap-2">
          <div className="relative w-[260px]">
            <Search
              aria-hidden
              className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              aria-label="Поиск отказанных доменов"
              className="pl-8"
              placeholder="Найти домен"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setLimit(PAGE);
              }}
            />
          </div>
          {picked.length > 0 && (
            <>
              <span className="text-sm text-muted-foreground">
                Выбрано {picked.length}
              </span>
              <Button size="sm" onClick={() => void allow(picked)}>
                <Plus aria-hidden /> Разрешить
              </Button>
              {picked.some((n) => n.status !== "seen") && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void mark(picked, "seen")}
                >
                  <Check aria-hidden /> Просмотрено
                </Button>
              )}
              {picked.some((n) => n.status !== "ignored") && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void mark(picked, "ignored")}
                >
                  <EyeOff aria-hidden /> Игнорировать
                </Button>
              )}
              {picked.some((n) => n.status !== "new") && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => void mark(picked, "new")}
                >
                  <RotateCcw aria-hidden /> В новые
                </Button>
              )}
              {canBlock && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-bad"
                  onClick={() => void block(picked)}
                >
                  <Ban aria-hidden /> Запретить для всех
                </Button>
              )}
            </>
          )}
        </div>
        <Table className="w-full table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">
                <Checkbox
                  aria-label="Выбрать все"
                  checked={allPicked}
                  onCheckedChange={(on) =>
                    setSelected((s) => {
                      const next = new Set(s);
                      for (const n of shown) {
                        if (on) next.add(n.id);
                        else next.delete(n.id);
                      }
                      return next;
                    })
                  }
                />
              </TableHead>
              <TableHead>Домен</TableHead>
              <TableHead className="w-[68px] text-right">Отказов</TableHead>
              <TableHead className="w-[76px]">Последний</TableHead>
              {filter === 'all' && <TableHead className="w-[104px]">Статус</TableHead>}
              <TableHead className="w-[150px]" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((n) => {
              const host = n.host ?? "";
              const drafted = inDraft(host);
              return (
                <TableRow
                  key={n.id}
                  data-state={selected.has(n.id) ? "selected" : undefined}
                >
                  <TableCell>
                    <Checkbox
                      aria-label={`Выбрать ${host}`}
                      checked={selected.has(n.id)}
                      onCheckedChange={(on) => toggle(n.id, on === true)}
                    />
                  </TableCell>
                  <TableCell className="truncate font-mono text-sm" title={host}>
                    {host}
                    {drafted && (
                      <Badge variant="mod" className="ml-1.5 font-sans">
                        в черновике
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {n.denied.toLocaleString("ru-RU")}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    <Timestamp value={n.last_seen} mode="relative"/>
                  </TableCell>
                  {filter === 'all' && <TableCell className="truncate">{statusBadge(n)}</TableCell>}
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      {!drafted && (
                        <Button
                          size="xs"
                          variant="outline"
                          onClick={() => void allow([n])}
                        >
                          <Plus aria-hidden /> Разрешить
                        </Button>
                      )}
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            size="icon-xs"
                            variant="ghost"
                            aria-label={`Действия с ${host}`}
                          >
                            <MoreHorizontal aria-hidden />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {n.status !== "seen" && (
                            <DropdownMenuItem
                              onSelect={() => void mark([n], "seen")}
                            >
                              <Check aria-hidden /> Просмотрено
                            </DropdownMenuItem>
                          )}
                          {n.status !== "ignored" && (
                            <DropdownMenuItem
                              onSelect={() => void mark([n], "ignored")}
                            >
                              <EyeOff aria-hidden /> Игнорировать
                            </DropdownMenuItem>
                          )}
                          {n.status !== "new" && (
                            <DropdownMenuItem
                              onSelect={() => void mark([n], "new")}
                            >
                              <RotateCcw aria-hidden /> В новые
                            </DropdownMenuItem>
                          )}
                          {canBlock && (
                            <>
                              <DropdownMenuSeparator />
                              <DropdownMenuItem
                                variant="destructive"
                                onSelect={() => void block([n])}
                              >
                                <Ban aria-hidden /> Запретить для всех
                              </DropdownMenuItem>
                            </>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
            {shown.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={filter === 'all' ? 6 : 5}
                  className="text-sm text-muted-foreground"
                >
                  {query ? "Домены не найдены" : filter === 'new' ? "Новых отказов нет" : filter === 'seen' ? "Просмотренных доменов нет" : filter === 'ignored' ? "Игнорируемых доменов нет" : "Отказанных доменов нет"}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
        {rows.length > limit && (
          <Button
            size="sm"
            variant="ghost"
            className="self-start"
            onClick={() => setLimit((l) => l + PAGE)}
          >
            Показать ещё {Math.min(PAGE, rows.length - limit)} из{" "}
            {rows.length - limit}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
