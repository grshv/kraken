import { useEffect, useMemo, useState } from "react";
import { History, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import type { Preview, RevisionSummary } from "../model";
import type { NavId } from "../shell/nav";
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
  const { request, session, onToast, onNavigate } = props;
  const canConfigure =
    session.permissions?.includes("configure") ?? false;
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
    <div className="placeholder-section">
      <aside className="list-pane">
        <div className="list-head">
          <span className="list-title">Аудит</span>
          <div className="search-input">
            <Search aria-hidden />
            <Input
              aria-label="Поиск по актору"
              placeholder="Кто, что, ревизия…"
              value={actor}
              onChange={(e) => setActor(e.target.value)}
            />
          </div>
          <div className="row" style={{ gap: 6, flexWrap: "wrap" }}>
            {AUDIT_FILTERS.map((f) => (
              <button
                key={f.id}
                type="button"
                className={"badge" + (category === f.id ? " badge-selected" : "")}
                aria-pressed={category === f.id}
                onClick={() => setCategory(f.id)}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>
        <div className="list-body">
          {loadError ? (
            <div className="list-muted">
              <ErrorState
                title="Аудит недоступен"
                detail="Не удалось загрузить события. Повторяем запрос…"
                onRetry={() => void fetchFirst()}
              />
            </div>
          ) : groups.length === 0 && !loading ? (
            <EmptyState icon={<History />} title="Событий нет">
              За выбранный период и фильтр аудит пуст.
            </EmptyState>
          ) : (
            groups.map((g) => (
              <div key={g.day}>
                <div className="list-group-label">{g.day}</div>
                {g.items.map((a) => (
                  <button
                    key={a.id}
                    className={
                      "list-item audit-item" +
                      (selectedID === a.id ? " selected" : "")
                    }
                    onClick={() => setSelectedID(a.id)}
                  >
                    <span>
                      <div className="ellipsis">{actionLabel(a.action)}</div>
                      <div className="xs muted-text">{a.actor}</div>
                    </span>
                    <span className="xs muted-text num">
                      {a.timestamp.slice(11, 16)}
                    </span>
                  </button>
                ))}
              </div>
            ))
          )}
          {gap && !loadError && (
            <div className="list-muted small">
              В аудите есть пропуски: возможно, старые записи удалены.
            </div>
          )}
          {cursor && !loadError && (
            <div className="row" style={{ justifyContent: "center", padding: 12 }}>
              <Button size="xs" variant="ghost" onClick={loadMore}>
                Ещё
              </Button>
            </div>
          )}
        </div>
      </aside>
      <section className="inspector-pane">
        {selected ? (
          <AuditDetail
            event={selected}
            request={request}
            canConfigure={canConfigure}
            onNavigate={onNavigate}
          />
        ) : (
          <div className="inspector-body">
            <EmptyState icon={<History />} title="Выберите событие">
              Выберите запись слева, чтобы увидеть детали действия.
            </EmptyState>
          </div>
        )}
      </section>
    </div>
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

// AuditDetail renders the actor/timestamp/entity and, for revisions, fetches
// the preview and latest operation status admins can see.
function AuditDetail({
  event,
  request,
  canConfigure,
  onNavigate,
}: {
  event: AuditEvent;
  request: SectionProps["request"];
  canConfigure: boolean;
  onNavigate: (id: NavId) => void;
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
      <header className="inspector-head">
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <h1 className="page-title" style={{ margin: 0 }}>
            {actionLabel(event.action)}
          </h1>
          <Badge variant="secondary">{catLabel}</Badge>
        </div>
        <div className="muted-text small">
          {dayLabel(event.timestamp)}, {event.timestamp.slice(11, 19)} ·{" "}
          {event.actor}
        </div>
      </header>
      <div className="inspector-body">
        <div className="card">
          <div className="card-body">
            <dl className="kv-row">
              <dt>Кто</dt>
              <dd>{event.actor}</dd>
              <dt>Когда</dt>
              <dd className="num">
                {event.timestamp.replace("T", " ").slice(0, 19)}
              </dd>
              <dt>Объект</dt>
              <dd className="mono">{event.entity_id || "—"}</dd>
            </dl>
          </div>
        </div>

        {isRevision && (
          <div className="card">
            <div className="card-head">
              <span className="card-title grow">Что изменилось</span>
              <span className="mono muted-text small">
                {event.entity_id.slice(0, 8)}
              </span>
            </div>
            <div className="card-body">
              {canConfigure ? (
                failed ? (
                  <div className="muted-text small">
                    Не удалось загрузить preview ревизии.
                  </div>
                ) : preview ? (
                  preview.summary.length > 0 ? (
                    <ul className="md-ul" style={{ margin: 0 }}>
                      {preview.summary.map((s, i) => (
                        <li key={i}>{s}</li>
                      ))}
                    </ul>
                  ) : (
                    <span className="muted-text small">Без пользовательских изменений</span>
                  )
                ) : (
                  <span className="muted-text small">Загружаем…</span>
                )
              ) : (
                <div className="muted-text small">
                  Просмотр изменений ревизии доступен администратору.
                </div>
              )}
              {revision && (
                <div className="row" style={{ marginTop: 10, gap: 8, flexWrap: "wrap" }}>
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
            </div>
          </div>
        )}

        <div className="muted-text small">
          Аудит хранит 90 дней или 5 000 событий. Пароли и содержимое запросов не
          записываются.
        </div>
        <div className="row">
          <Button size="xs" variant="ghost" onClick={() => onNavigate("audit")}>
            К списку
          </Button>
        </div>
      </div>
    </>
  );
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
