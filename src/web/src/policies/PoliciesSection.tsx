import { useEffect, useId, useMemo, useState } from "react";
import { Copy, Pencil, Plus, ShieldCheck, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListRow,
  InspectorPane,
  InspectorHeader,
  InspectorBody,
} from "@/components/kraken/layout";
import { Callout, Field } from "@/components/kraken/blocks";
import { EmptyState } from "../shell/EmptyState";
import { DomainEditor, type DomainHits } from "./DomainEditor";
import { missingDomains, type HostCount } from "../clients/missing";
import {
  managerScope,
  type CommitResult,
  type SectionProps,
} from "../sections/sectionProps";
import { entityMarks, type Mark } from "../draft/marks";
import {
  addDomainsToPolicy,
  deletePolicy,
  newEntityID,
  renamePolicy,
} from "../draft/edits";
import type { Client, Policy, Session, Snapshot } from "../model";
import { pluralRu } from "../model";
import { BlockedDomainEditor } from "../settings/BlockedDomainEditor";
import { setBlockedDomains } from "../settings/edits";

// BLOCKED selects the pinned global deny list instead of a policy.
const BLOCKED = "__blocked";

// PoliciesSection: named domain sets with client/domain counts and a detail
// editor (domains, 24h/7d hits, clients). A client attaches any number of
// sets; access mode and transport belong to the client. Delete is allowed
// only when no client attaches the policy; a manager cannot change a policy
// used by foreign clients.
export function PoliciesSection(props: SectionProps) {
  const {
    request,
    draft,
    active,
    session,
    onCommit,
    onToast,
    onOpen,
    intent,
    onIntentHandled,
  } = props;
  const [selected, setSelected] = useState<string | null>(null);
  const [hits, setHits] = useState<DomainHits>({});
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  // 24h request totals per policy for the list.
  const [totals, setTotals] = useState<Record<string, number>>({});

  const snap = draft?.snapshot;
  const policies = snap?.policies ?? [];
  const marks = useMemo(
    () => entityMarks(active?.policies ?? [], policies),
    [active, policies],
  );
  const clientsOf = (id: string) =>
    (snap?.clients ?? []).filter((c) => c.policy_ids.includes(id));

  const selectedPolicy = policies.find((p) => p.id === selected) ?? null;
  // Only an admin edits the global deny list.
  const canBlock = session.role !== "manager";
  const blocked = snap?.settings?.blocked_domains ?? [];

  useEffect(() => {
    if (!selectedPolicy) return;
    let live = true;
    request<{
      domains: {
        domain: string;
        include_subdomains: boolean;
        hits_24h: number;
        hits_7d: number;
        denied_24h?: number;
      }[];
    }>(`/inspector/policies/${selectedPolicy.id}/hits`)
      .then((r) => {
        if (!live) return;
        const next: DomainHits = {};
        for (const d of r.domains ?? [])
          next[d.domain] = {
            hits_24h: d.hits_24h,
            hits_7d: d.hits_7d,
            denied_24h: d.denied_24h,
          };
        setHits(next);
      })
      .catch(() => setHits({}));
    return () => {
      live = false;
    };
  }, [request, selectedPolicy?.id]);

  // "policy:<id>" from another section selects that policy.
  useEffect(() => {
    if (intent === "blocked-domains") {
      setCreating(false);
      setSelected(BLOCKED);
      onIntentHandled?.();
      return;
    }
    if (!intent?.startsWith("policy:")) return;
    setCreating(false);
    setSelected(intent.slice("policy:".length));
    onIntentHandled?.();
  }, [intent, onIntentHandled]);

  useEffect(() => {
    let live = true;
    const ids = policies.map((p) => p.id).slice(0, 50);
    Promise.all(
      ids.map((id) =>
        request<{ domains: { hits_24h: number }[] }>(
          `/inspector/policies/${id}/hits`,
        )
          .then(
            (r) => [id, r.domains.reduce((n, d) => n + d.hits_24h, 0)] as const,
          )
          .catch(() => [id, 0] as const),
      ),
    ).then((rows) => {
      if (live) setTotals(Object.fromEntries(rows));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request, policies.map((p) => p.id).join(",")]);

  // Select the first policy when the section opens with no selection.
  useEffect(() => {
    if (selected === null && policies.length > 0 && !creating) {
      setSelected(policies[0].id);
    }
  }, [selected, policies, creating]);

  if (policies.length === 0 && !creating) {
    return (
      <>
        <ListPane>
          <ListPaneHeader
            title="Политики"
            action={
              <Button size="xs" onClick={() => setCreating(true)}>
                <Plus aria-hidden /> Новая
              </Button>
            }
          />
          <ListPaneBody>
            <div className="px-3.5 py-3 text-sm text-muted-foreground">
              Политик пока нет.
            </div>
          </ListPaneBody>
        </ListPane>
        <InspectorPane>
          <InspectorBody>
            <EmptyState icon={<ShieldCheck />} title="Нет политик">
              Политика — именованный набор доменов. Создайте её и подключите
              клиентам, которым нужны одни и те же сайты.
            </EmptyState>
          </InspectorBody>
        </InspectorPane>
      </>
    );
  }

  const createForm = creating && snap && (
    <NewPolicyForm
      snap={snap!}
      newName={newName}
      onName={setNewName}
      onCancel={() => setCreating(false)}
      onSubmit={async () => {
        const name = newName.trim();
        if (!name) {
          onToast("Укажите имя политики");
          return;
        }
        const id = newEntityID(snap!, "p-");
        const p: Policy = { id, name, destinations: [] };
        const res = await onCommit({
          ...snap!,
          policies: [...snap!.policies, p],
        });
        if (res.ok) {
          setSelected(id);
          setCreating(false);
          setNewName("");
          onToast("Политика создана в черновике");
        }
      }}
    />
  );

  return (
    <>
      <ListPane>
        <ListPaneHeader
          title="Политики"
          action={
            <Button size="xs" onClick={() => setCreating(true)}>
              <Plus aria-hidden /> Новая
            </Button>
          }
          description="Именованные наборы доменов. Клиент подключает любое число наборов."
        />
        <ListPaneBody>
          {canBlock && (
            <ListRow
              selected={!creating && selected === BLOCKED}
              onClick={() => {
                setCreating(false);
                setSelected(BLOCKED);
              }}
              title={<span className="font-medium">Запрещено для всех</span>}
              meta={
                <span className="text-muted-foreground">
                  {pluralRu(blocked.length, "домен", "домена", "доменов")}
                </span>
              }
              subtitle="Действует на всех клиентов, включая «весь веб»"
            />
          )}
          {policies.map((p) => (
            <PolicyRow
              key={p.id}
              policy={p}
              clients={clientsOf(p.id).length}
              domains={p.destinations.length}
              total={totals[p.id]}
              mark={marks[p.id]}
              active={!creating && selected === p.id}
              onClick={() => {
                setCreating(false);
                setSelected(p.id);
              }}
            />
          ))}
          {policies.length === 0 && (
            <div className="px-3.5 py-3 text-sm text-muted-foreground">
              Политик нет
            </div>
          )}
        </ListPaneBody>
      </ListPane>
      {createForm || (
        <InspectorPane>
          {selected === BLOCKED && snap && canBlock ? (
            <BlockedDetail
              snap={snap}
              changed={
                JSON.stringify(active?.settings?.blocked_domains ?? []) !==
                JSON.stringify(blocked)
              }
              onCommit={onCommit}
              onToast={onToast}
            />
          ) : selectedPolicy ? (
            <PolicyDetail
              policy={selectedPolicy}
              snap={snap!}
              clients={clientsOf(selectedPolicy.id)}
              hits={hits}
              mark={marks[selectedPolicy.id]}
              session={session}
              request={request}
              onOpen={onOpen}
              onCommit={onCommit}
              onToast={onToast}
              onDelete={async () => {
                if (clientsOf(selectedPolicy.id).length > 0) {
                  onToast("Сначала отключите политику у клиентов");
                  return;
                }
                const res = await onCommit(
                  deletePolicy(snap!, selectedPolicy.id),
                );
                if (res.ok) {
                  setSelected(null);
                  onToast("Политика удалена");
                }
              }}
              onDuplicate={async () => {
                const id = newEntityID(snap!, "p-");
                const copy: Policy = {
                  id,
                  name: selectedPolicy.name + "-копия",
                  destinations: selectedPolicy.destinations.map((d) => ({
                    ...d,
                  })),
                };
                const res = await onCommit({
                  ...snap!,
                  policies: [...snap!.policies, copy],
                });
                if (res.ok) {
                  setSelected(id);
                  onToast("Политика дублирована");
                }
              }}
            />
          ) : (
            <InspectorBody>
              <EmptyState icon={<ShieldCheck />} title="Выберите политику">
                Выберите политику слева или создайте новую.
              </EmptyState>
            </InspectorBody>
          )}
        </InspectorPane>
      )}
    </>
  );
}

function PolicyRow({
  policy,
  clients,
  domains,
  total,
  mark,
  active,
  onClick,
}: {
  policy: Policy;
  clients: number;
  domains: number;
  total?: number;
  mark?: Mark;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <ListRow
      selected={active}
      onClick={onClick}
      title={
        <span className="font-medium">
          {policy.name}
          {mark === "new" && <Badge variant="add">новая</Badge>}
          {mark === "changed" && <Badge variant="mod">изменена</Badge>}
        </span>
      }
      meta={
        <span className="text-muted-foreground">
          {clients
            ? pluralRu(clients, "клиент", "клиента", "клиентов")
            : "не подключена"}
        </span>
      }
      subtitle={pluralRu(domains, "домен", "домена", "доменов")}
      aside={
        total ? (
          <span className="text-muted-foreground">
            {total.toLocaleString("ru-RU")}
          </span>
        ) : undefined
      }
    />
  );
}

function PolicyDetail({
  policy,
  snap,
  clients,
  hits,
  mark,
  session,
  request,
  onOpen,
  onCommit,
  onToast,
  onDelete,
  onDuplicate,
}: {
  policy: Policy;
  snap: Snapshot;
  clients: Client[];
  hits: DomainHits;
  mark?: Mark;
  session: Session;
  request: SectionProps["request"];
  onOpen?: SectionProps["onOpen"];
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onDelete: () => Promise<void>;
  onDuplicate: () => Promise<void>;
}) {
  const [editingName, setEditingName] = useState(false);
  const [name, setName] = useState(policy.name);
  const scope = managerScope(session);
  // Manager may edit only if every client referencing the policy is his.
  const editable =
    !scope || clients.every((c) => scope.clientIDs.includes(c.id));
  const hasClients = clients.length > 0;
  const nameId = useId();
  return (
    <>
      <InspectorHeader
        title={
          editingName ? (
            <form
              className="flex items-center gap-2"
              onSubmit={async (e) => {
                e.preventDefault();
                const res = await onCommit(
                  renamePolicy(snap, policy.id, name.trim() || policy.name),
                );
                if (res.ok) setEditingName(false);
              }}
            >
              <Input
                id={nameId}
                aria-label="Название"
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape") setEditingName(false);
                }}
                className="w-[240px]"
              />
              <Button size="xs" type="submit">
                Сохранить
              </Button>
            </form>
          ) : (
            policy.name
          )
        }
        status={
          <Badge variant="secondary">
            {pluralRu(policy.destinations.length, "домен", "домена", "доменов")}
          </Badge>
        }
        actions={
          <>
            {mark === "new" && <Badge variant="add">новая, в черновике</Badge>}
            {mark === "changed" && <Badge variant="mod">изменена</Badge>}
            {!editingName && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => setEditingName(true)}
                disabled={!editable}
              >
                <Pencil aria-hidden /> Переименовать
              </Button>
            )}
            <Button size="xs" variant="outline" onClick={onDuplicate}>
              <Copy aria-hidden /> Дублировать
            </Button>
            <Button
              size="xs"
              variant="outline"
              className="text-bad"
              onClick={onDelete}
              disabled={hasClients || !editable}
              title={
                hasClients
                  ? "Сначала отключите политику у клиентов"
                  : !editable
                    ? "Политика используется чужими клиентами"
                    : undefined
              }
            >
              Удалить
            </Button>
          </>
        }
        meta={
          <>
            <span>Подключена у:</span>
            {hasClients ? (
              clients.map((c) => (
                <Button
                  key={c.id}
                  size="xs"
                  variant="outline"
                  onClick={() => onOpen?.("clients", `client:${c.id}`)}
                >
                  {c.name}
                </Button>
              ))
            ) : (
              <span>никто</span>
            )}
          </>
        }
      />
      <InspectorBody>
        {clients.length > 1 && (
          <Callout tone="warn" icon={<TriangleAlert aria-hidden />}>
            <div className="text-sm">
              Клиенты, которых затронут изменения:{" "}
              {pluralRu(clients.length, "клиент", "клиента", "клиентов")}:{" "}
              {clients.map((c) => c.name).join(", ")}.
            </div>
          </Callout>
        )}
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Домены</CardTitle>
              <CardDescription>
                «Не используется»: ни одного запроса за 7 дней
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <DomainEditor
              destinations={policy.destinations}
              hits={hits}
              readOnly={!editable}
              readOnlyReason="Политика используется чужими клиентами. Изменить её может администратор."
              onChange={async (next) => {
                const res = await onCommit({
                  ...snap,
                  policies: snap.policies.map((p) =>
                    p.id === policy.id ? { ...p, destinations: next } : p,
                  ),
                });
                if (!res.ok && res.code !== "revision_conflict")
                  onToast(res.message ?? "Ошибка");
              }}
            />
            <div className="mt-2.5 flex items-center gap-1.5">
              <TemplateButtons
                request={request}
                onAdd={async (doms) => {
                  const res = await onCommit(
                    addDomainsToPolicy(snap, policy.id, doms),
                  );
                  if (res.ok) onToast("Шаблон добавлен");
                }}
              />
            </div>
          </CardContent>
        </Card>
        <MissingCard
          policy={policy}
          clients={clients}
          snap={snap}
          request={request}
          editable={editable}
          onCommit={onCommit}
          onToast={onToast}
        />
      </InspectorBody>
    </>
  );
}

// MissingCard: denied domains of this policy's list-access clients over 24 h
// that their final list does not cover, each with "Добавить" into this policy.
function MissingCard({
  policy,
  clients,
  snap,
  request,
  editable,
  onCommit,
  onToast,
}: {
  policy: Policy;
  clients: Client[];
  snap: Snapshot;
  request: SectionProps["request"];
  editable: boolean;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
}) {
  const [rows, setRows] = useState<
    { host: string; denied: number; client: string }[] | null
  >(null);
  const ids = clients.map((c) => c.id).join(",");
  useEffect(() => {
    let live = true;
    Promise.all(
      clients
        .filter((c) => c.access === "list")
        .slice(0, 20)
        .map((c) =>
          request<{ top_destinations: HostCount[] }>(
            `/inspector/clients/${c.id}/stats`,
          )
            .then((st) =>
              missingDomains(st.top_destinations, snap, c).map((d) => ({
                host: d.host,
                denied: d.denied,
                client: c.name,
              })),
            )
            .catch(() => []),
        ),
    ).then((all) => {
      if (live) setRows(all.flat().sort((a, b) => b.denied - a.denied));
    });
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request, ids, snap]);
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Чего не хватает</CardTitle>
          <CardDescription>
            Отказанные домены клиентов этой политики за 24 ч
          </CardDescription>
        </div>
      </CardHeader>
      <CardContent>
        {rows === null ? null : rows.length === 0 ? (
          <div className="text-sm text-muted-foreground">Отказов нет</div>
        ) : (
          rows.slice(0, 10).map((r) => (
            <div
              key={r.host + r.client}
              className="flex items-center gap-2.5 border-b py-1.5 last:border-b-0"
            >
              <span className="grow truncate font-mono text-sm">{r.host}</span>
              <span className="text-sm text-muted-foreground">{r.client}</span>
              <span className="w-[76px] text-right text-sm text-bad tabular-nums">
                {pluralRu(r.denied, "отказ", "отказа", "отказов")}
              </span>
              <Button
                size="xs"
                variant="outline"
                disabled={!editable}
                onClick={async () => {
                  const res = await onCommit(
                    addDomainsToPolicy(snap, policy.id, [r.host]),
                  );
                  if (res.ok) onToast(`Домен ${r.host} добавлен в политику`);
                }}
              >
                <Plus aria-hidden /> Добавить
              </Button>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

export function TemplateButtons({
  request,
  onAdd,
}: {
  request: SectionProps["request"];
  onAdd: (domains: string[]) => void;
}) {
  const [templates, setTemplates] = useState<
    { id: string; name: string; destinations: { domain: string }[] }[]
  >([]);
  useEffect(() => {
    let live = true;
    request<{
      templates: {
        id: string;
        name: string;
        destinations: { domain: string }[];
      }[];
    }>("/inspector/domain-templates")
      .then((r) => {
        if (live) setTemplates(r.templates ?? []);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [request]);
  return (
    <>
      {templates.map((t) => (
        <Button
          key={t.id}
          size="xs"
          variant="outline"
          onClick={() => onAdd(t.destinations.map((d) => d.domain))}
        >
          {t.name}
        </Button>
      ))}
    </>
  );
}

function NewPolicyForm({
  snap,
  newName,
  onName,
  onCancel,
  onSubmit,
}: {
  snap: Snapshot;
  newName: string;
  onName: (s: string) => void;
  onCancel: () => void;
  onSubmit: () => Promise<void>;
}) {
  return (
    <InspectorPane>
      <InspectorHeader title="Новая политика" />
      <InspectorBody>
        <Card>
          <CardContent>
            <Field label="Название" htmlFor="f-np-name">
              <Input
                id="f-np-name"
                value={newName}
                onChange={(e) => onName(e.target.value)}
                placeholder="например, build-runners"
              />
            </Field>
            <div className="mt-3 flex justify-end gap-2">
              <Button variant="ghost" onClick={onCancel}>
                Отмена
              </Button>
              <Button onClick={onSubmit}>
                <Plus aria-hidden /> Добавить в черновик
              </Button>
            </div>
          </CardContent>
        </Card>
      </InspectorBody>
    </InspectorPane>
  );
}

// BlockedDetail: the global deny list. It is checked right after the built-in
// protection, before any client rule, so it also closes «весь веб».
function BlockedDetail({
  snap,
  changed,
  onCommit,
  onToast,
}: {
  snap: Snapshot;
  changed: boolean;
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
}) {
  const blocked = snap.settings?.blocked_domains ?? [];
  return (
    <>
      <InspectorHeader
        title={
          <span className="flex items-center gap-2">
            Запрещено для всех
            {changed && <Badge variant="mod">изменено</Badge>}
          </span>
        }
      />
      <InspectorBody>
        <Callout tone="info">
          <span className="text-sm">
            Домены закрыты для всех клиентов, включая «весь веб» и squid.conf
            вручную. Проверяются сразу после встроенной защиты и до правил
            клиента.
          </span>
        </Callout>
        <Card>
          <CardHeader>
            <CardTitle>Домены</CardTitle>
            <CardDescription>
              {pluralRu(snap.clients.length, "клиент", "клиента", "клиентов")}{" "}
              получат отказ к этим доменам
            </CardDescription>
          </CardHeader>
          <CardContent>
            <BlockedDomainEditor
              destinations={blocked}
              onChange={async (next) => {
                const res = await onCommit(setBlockedDomains(snap, next));
                if (res.ok) onToast("Список обновлён в черновике");
              }}
            />
          </CardContent>
        </Card>
      </InspectorBody>
    </>
  );
}
