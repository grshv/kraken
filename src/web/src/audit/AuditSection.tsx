import { Timestamp } from "@/components/kraken/timestamp";
import { useEffect, useMemo, useState } from "react";
import {
  Diff,
  History,
  KeyRound,
  ListFilter,
  Play,
  Search,
  User,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "cn";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  InspectorBody,
  InspectorHeader,
  InspectorPane,
  ListGroupLabel,
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListRow,
} from "@/components/kraken/layout";
import { KeyValue } from "@/components/kraken/blocks";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import type { Preview, RevisionSummary } from "../model";
import { revisionLabel } from "../model";
import { ChangeList } from "../draft/ChangeList";
import type { SectionProps } from "../sections/sectionProps";
import {
  AUDIT_FILTERS,
  actionCategory,
  actionLabel,
  type AuditCategory,
} from "./actions";

// AuditEvent/AuditPage mirror the GET /audit response fields.
type AuditEvent = {
  id: string;
  actor: string;
  action: string;
  entity_id: string;
  // detail is the server summary ("Клиент «x» изменён"); empty for actions
  // without one. Untrusted display text.
  detail?: string;
  timestamp: string;
};
type AuditPage = {
  items: AuditEvent[];
  next_cursor: string | null;
  gap: boolean;
  observed_at: string;
};

const PAGE_LIMIT = 100;

// AuditSection: chronological audit trail filtered by actor and category. The
// detail pane reads the revision (admin only) for changes and operation status.
export function AuditSection(props: SectionProps) {
  const { request, session, onToast } = props;
  const canConfigure = session.permissions?.includes("configure") ?? false;
  const [actor, setActor] = useState("");
  const [category, setCategory] = useState<AuditCategory | "all">("all");
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [gap, setGap] = useState(false);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [selectedID, setSelectedID] = useState<string | null>(null);

  const query = useMemo(() => {
    const sp = new URLSearchParams();
    sp.set("limit", String(PAGE_LIMIT));
    if (actor.trim()) sp.set("actor", actor.trim());
    return sp.toString();
  }, [actor]);

  const fetchFirst = async (signal?: AbortSignal) => {
    setLoading(true);
    setLoadError(false);
    try {
      const r = await request<AuditPage>(
        `/audit?${query}`,
        "GET",
        undefined,
        undefined,
        signal,
      );
      setItems(r.items ?? []);
      setCursor(r.next_cursor);
      setGap(!!r.gap);
    } catch (e) {
      if ((e as Error).name === "AbortError") return;
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      void fetchFirst(ctrl.signal);
    }, 250);
    setSelectedID(null);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  // Keep the first event selected when the list loads with nothing picked, so
  // the detail pane is populated on first open (the query effect clears the
  // selection on every actor-search change).
  useEffect(() => {
    if (selectedID === null && items.length > 0) {
      setSelectedID(items[0].id);
    }
  }, [items, selectedID]);

  const loadMore = async () => {
    if (!cursor) return;
    const sp = new URLSearchParams(query);
    sp.set("cursor", cursor);
    try {
      const r = await request<AuditPage>(`/audit?${sp.toString()}`);
      setItems((prev) => [...prev, ...(r.items ?? [])]);
      setCursor(r.next_cursor);
      setGap(!!r.gap);
    } catch {
      onToast("Не удалось подгрузить аудит");
    }
  };

  const filtered = items.filter((a) =>
    category === "all" ? true : actionCategory(a.action) === category,
  );
  const groups = groupByDay(filtered);
  const selected = filtered.find((a) => a.id === selectedID) ?? null;

  return (
    <>
      <ListPane>
        <ListPaneHeader title="Аудит">
          <div className="flex items-center gap-2">
            <div className="relative flex min-w-0 flex-1 items-center">
              <Search
                aria-hidden
                className="pointer-events-none absolute left-2.5 size-3.5 text-muted-foreground"
              />
              <Input
                aria-label="Поиск по актору"
                className="h-8 pl-[30px]"
                placeholder="Имя пользователя"
                value={actor}
                onChange={(e) => setActor(e.target.value)}
              />
            </div>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="outline"
                  size="icon"
                  className={cn(
                    "relative size-8 shrink-0",
                    category !== "all" && "border-ring bg-sel text-primary",
                  )}
                  aria-label="Фильтры аудита"
                  title={AUDIT_FILTERS.find((f) => f.id === category)?.label}
                >
                  <ListFilter aria-hidden />
                  {category !== "all" && (
                    <span
                      className="absolute top-1 right-1 size-1.5 rounded-full bg-primary"
                      aria-hidden
                    />
                  )}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuRadioGroup
                  value={category}
                  onValueChange={(v) => setCategory(v as AuditCategory | "all")}
                >
                  {AUDIT_FILTERS.map((f) => (
                    <DropdownMenuRadioItem key={f.id} value={f.id}>
                      {f.label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </ListPaneHeader>
        <ListPaneBody>
          {loadError ? (
            <div className="px-3.5 py-4 text-sm text-muted-foreground">
              <ErrorState
                title="Аудит недоступен"
                detail="Не удалось загрузить события. Повторяем запрос…"
                onRetry={() => void fetchFirst()}
              />
            </div>
          ) : groups.length === 0 && !loading ? (
            <EmptyState icon={<History />} title="Событий нет">
              Нет событий по текущему фильтру.
            </EmptyState>
          ) : (
            groups.map((g) => (
              <div key={g.day}>
                <ListGroupLabel>{g.day}</ListGroupLabel>
                {g.items.map((a) => {
                  const Icon = auditCategoryIcon(actionCategory(a.action));
                  return (
                    <ListRow
                      key={a.id}
                      selected={selectedID === a.id}
                      leading={<Icon aria-hidden />}
                      title={a.detail || actionLabel(a.action)}
                      meta={
                        <Timestamp
                          value={a.timestamp}
                          mode="time"
                          seconds={false}
                        />
                      }
                      subtitle={
                        a.detail
                          ? `${actionLabel(a.action)} · ${a.actor}`
                          : a.actor
                      }
                      onClick={() => setSelectedID(a.id)}
                    />
                  );
                })}
              </div>
            ))
          )}
          {gap && !loadError && (
            <div className="px-3.5 py-4 text-sm text-muted-foreground">
              В аудите есть пропуски: возможно, старые записи удалены.
            </div>
          )}
          {cursor && !loadError && (
            <div className="flex justify-center p-3">
              <Button size="xs" variant="ghost" onClick={loadMore}>
                Ещё
              </Button>
            </div>
          )}
        </ListPaneBody>
      </ListPane>
      <InspectorPane>
        {selected ? (
          <AuditDetail
            event={selected}
            request={request}
            canConfigure={canConfigure}
          />
        ) : (
          <InspectorBody>
            <EmptyState icon={<History />} title="Выберите событие">
              Выберите запись слева, чтобы увидеть детали действия.
            </EmptyState>
          </InspectorBody>
        )}
      </InspectorPane>
    </>
  );
}

type DayGroup = { day: string; items: AuditEvent[] };

function groupByDay(items: AuditEvent[]): DayGroup[] {
  const groups: DayGroup[] = [];
  let day = "";
  for (const a of items) {
    const label = dayLabel(a.timestamp);
    if (label !== day) {
      day = label;
      groups.push({ day: label, items: [] });
    }
    groups[groups.length - 1].items.push(a);
  }
  return groups;
}

function dayLabel(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const day = new Date(d);
  day.setHours(0, 0, 0, 0);
  const diff = Math.round((today.getTime() - day.getTime()) / 86400000);
  if (diff === 0) return "Сегодня";
  if (diff === 1) return "Вчера";
  return d.toLocaleDateString("ru-RU", { day: "numeric", month: "short" });
}

// Map an audit category to a status-style icon for the list row's leading
// slot, with per-category icons (apply → play, login → user,
// account → key, draft → diff, other → history).
function auditCategoryIcon(cat: AuditCategory) {
  switch (cat) {
    case "apply":
      return Play;
    case "login":
      return User;
    case "account":
      return KeyRound;
    case "draft":
      return Diff;
    case "other":
      return History;
  }
}

// AuditDetail renders the actor/timestamp/entity and, for revisions, fetches
// the preview and latest operation status admins can see.
function AuditDetail({
  event,
  request,
  canConfigure,
}: {
  event: AuditEvent;
  request: SectionProps["request"];
  canConfigure: boolean;
}) {
  const isRevision = event.action === "create_revision";
  const [revision, setRevision] = useState<RevisionSummary | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!isRevision || !canConfigure) {
      setRevision(null);
      setPreview(null);
      setFailed(false);
      return;
    }
    let live = true;
    setRevision(null);
    setPreview(null);
    setFailed(false);
    // Revision summary list is small; we look up latest_status and rollback
    // eligibility by id rather than decoding per-revision internals.
    request<{ items: RevisionSummary[] }>(`/revisions?limit=100`)
      .then((r) => {
        if (!live) return;
        const found = (r.items ?? []).find((x) => x.id === event.entity_id);
        if (live && found) setRevision(found);
      })
      .catch(() => {});
    request<Preview>(
      `/revisions/${encodeURIComponent(event.entity_id)}/preview`,
    )
      .then((r) => {
        if (!live) return;
        setPreview(r);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [event.id, event.entity_id, isRevision, canConfigure, request]);

  const isDraftChange =
    event.action === "update_draft" || event.action === "discard_draft";
  const showChanges = isRevision || isDraftChange;
  const cat = actionCategory(event.action);
  const catLabel =
    cat === "apply"
      ? "Применение"
      : cat === "login"
        ? "Вход"
        : cat === "account"
          ? "Учётная запись"
          : cat === "draft"
            ? "Черновик"
            : "Другое";

  return (
    <>
      <InspectorHeader
        title={actionLabel(event.action)}
        status={
          <Badge variant={detailBadgeVariant(event, cat, revision)}>
            {catLabel}
          </Badge>
        }
        meta={
          <>
            <Timestamp value={event.timestamp} /> · {event.actor}
          </>
        }
      />
      <InspectorBody>
        <Card>
          <CardContent>
            <KeyValue
              items={[
                ...(event.detail && !showChanges
                  ? ([["Что", event.detail]] as [string, string][])
                  : []),
                ["Кто", event.actor],
                [
                  "Когда",
                  <span className="tabular-nums">
                    <Timestamp value={event.timestamp} />
                  </span>,
                ],
                [
                  "Объект",
                  isRevision ? (
                    <span className="font-mono">
                      {revisionLabel(event.entity_id, revision?.number)}
                    </span>
                  ) : event.entity_id === "draft" ? (
                    "черновик"
                  ) : (
                    <span className="font-mono">{event.entity_id || "—"}</span>
                  ),
                ],
              ]}
            />
          </CardContent>
        </Card>

        {isRevision && (
          <Card>
            <CardHeader>
              <CardTitle>Что изменилось</CardTitle>
              <CardAction>
                <span className="font-mono text-sm text-muted-foreground">
                  {revisionLabel(event.entity_id, revision?.number)}
                </span>
              </CardAction>
            </CardHeader>
            <CardContent>
              {canConfigure ? (
                failed ? (
                  <div className="text-sm text-muted-foreground">
                    Не удалось загрузить preview ревизии.
                  </div>
                ) : preview ? (
                  preview.summary.length > 0 ? (
                    <ChangeList items={preview.summary} />
                  ) : (
                    <span className="text-sm text-muted-foreground">
                      Без пользовательских изменений
                    </span>
                  )
                ) : (
                  <span className="text-sm text-muted-foreground">
                    Загружаем…
                  </span>
                )
              ) : event.detail ? (
                <ChangeList items={detailLines(event.detail)} />
              ) : (
                <div className="text-sm text-muted-foreground">
                  Просмотр изменений ревизии доступен администратору.
                </div>
              )}
              {revision && (
                <div className="mt-2.5 flex flex-wrap gap-2">
                  <Badge variant={statusBadge(revision.latest_status)}>
                    {statusLabel(revision.latest_status)}
                  </Badge>
                  {revision.is_current && (
                    <Badge variant="secondary">текущая</Badge>
                  )}
                  {revision.can_rollback && (
                    <Badge variant="secondary">можно откатить</Badge>
                  )}
                </div>
              )}
            </CardContent>
          </Card>
        )}

        {isDraftChange && (
          <Card>
            <CardHeader>
              <CardTitle>Что изменилось</CardTitle>
            </CardHeader>
            <CardContent>
              {event.detail ? (
                <ChangeList items={detailLines(event.detail)} />
              ) : (
                <div className="text-sm text-muted-foreground">
                  Подробности не записаны: событие старше записи сути изменений
                  в аудите.
                </div>
              )}
            </CardContent>
          </Card>
        )}
      </InspectorBody>
    </>
  );
}

// detailLines splits an audit summary ("a; b; c", see auditSummary in httpapi)
// into change lines.
export function detailLines(detail: string): string[] {
  return detail
    .split("; ")
    .map((l) => l.trim())
    .filter(Boolean);
}

function statusLabel(s: string): string {
  if (s === "succeeded") return "применено и подтверждено";
  if (s === "rolled_back") return "откат, трафик не затронут";
  if (s === "rejected") return "отклонено Squid";
  if (s === "validated") return "проверено";
  if (s === "degraded") return "применено с предупреждениями";
  if (s === "DRAFT") return "черновик";
  return s || "—";
}

function statusBadge(s: string): "default" | "destructive" | "secondary" {
  if (s === "succeeded") return "default";
  if (s === "rolled_back" || s === "rejected") return "destructive";
  return "secondary";
}

// Header summary badge: ok for a successful apply/login, bad for a rollback,
// error or failed login, secondary otherwise. For revisions we use the fetched
// latest_status when available (admin); a non-admin viewer sees secondary.
function detailBadgeVariant(
  event: AuditEvent,
  cat: AuditCategory,
  revision: RevisionSummary | null,
): "ok" | "bad" | "secondary" {
  if (cat === "login") {
    if (event.action === "login") return "ok";
    if (
      event.action === "login_failed" ||
      event.action === "csrf_failed" ||
      event.action === "rate_limited" ||
      event.action === "session_capacity_reached"
    )
      return "bad";
    return "secondary"; // logout
  }
  if (cat === "apply") {
    if (revision) {
      const s = revision.latest_status;
      if (s === "succeeded" || s === "validated" || s === "degraded")
        return "ok";
      if (s === "rolled_back" || s === "rejected") return "bad";
      return "secondary";
    }
    return "secondary";
  }
  return "secondary";
}
