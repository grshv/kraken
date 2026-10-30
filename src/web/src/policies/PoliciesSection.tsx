import { useEffect, useId, useMemo, useState } from "react";
import {
  Copy,
  Globe,
  Pencil,
  Plus,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListGroupLabel,
  ListRow,
  InspectorPane,
  InspectorHeader,
  InspectorBody,
} from "@/components/kraken/layout";
import { Callout, Field } from "@/components/kraken/blocks";
import { cn } from "cn";
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
  setPolicyMode,
} from "../draft/edits";
import type { Policy, Session, Snapshot } from "../model";
import { pluralRu } from "../model";

// PoliciesSection: list of named allowlists with client/domain counts and a
// detail editor (mode, domains, 24h/7d hits, clients). Delete is allowed only
// when no client references the policy; a manager cannot change a shared policy
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
    (snap?.clients ?? []).filter((c) => c.policy_id === id);

  const selectedPolicy = policies.find((p) => p.id === selected) ?? null;

  useEffect(() => {
    if (!selectedPolicy) return;
    let live = true;
    request<{
      domains: {
        domain: string;
        include_subdomains: boolean;
        hits_24h: number;
        hits_7d: number;
      }[];
    }>(`/inspector/policies/${selectedPolicy.id}/hits`)
      .then((r) => {
        if (!live) return;
        const next: DomainHits = {};
        for (const d of r.domains ?? [])
          next[d.domain] = { hits_24h: d.hits_24h, hits_7d: d.hits_7d };
        setHits(next);
      })
      .catch(() => setHits({}));
    return () => {
      live = false;
    };
  }, [request, selectedPolicy?.id]);

  // "policy:<id>" from another section selects that policy.
  useEffect(() => {
    if (!intent?.startsWith("policy:")) return;
    setCreating(false);
    setSelected(intent.slice("policy:".length));
    onIntentHandled?.();
  }, [intent, onIntentHandled]);

  useEffect(() => {
    let live = true;
    const ids = policies
      .filter((p) => p.mode === "allowlist")
      .map((p) => p.id)
      .slice(0, 50);
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
      <div className="flex w-full min-h-0">
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
              Создайте первую политику с набором доменов, затем назначьте её
              клиенту.
            </EmptyState>
          </InspectorBody>
        </InspectorPane>
      </div>
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
        const tlsID = snap!.tls_profiles[0]?.id ?? "";
        const p: Policy = {
          id,
          name,
          mode: "allowlist",
          destinations: [],
          tls_profile_id: tlsID,
        };
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

  const shared = policies.filter((p) => clientsOf(p.id).length !== 1);
  const own = policies.filter((p) => clientsOf(p.id).length === 1);

  return (
    <div className="flex w-full min-h-0">
      <ListPane>
        <ListPaneHeader
          title="Политики"
          action={
            <Button size="xs" onClick={() => setCreating(true)}>
              <Plus aria-hidden /> Новая
            </Button>
          }
          description="Именованные списки доменов. Клиент ссылается на одну политику."
        />
        <ListPaneBody>
          {shared.length > 0 && <ListGroupLabel>Общие</ListGroupLabel>}
          {shared.map((p) => (
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
          {own.length > 0 && <ListGroupLabel>Свои у клиентов</ListGroupLabel>}
          {own.map((p) => (
            <PolicyRow
              key={p.id}
              policy={p}
              clients={clientsOf(p.id).length}
              domains={p.destinations.length}
              own
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
          {selectedPolicy ? (
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
                  onToast("Сначала переназначьте клиентов");
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
                  mode: selectedPolicy.mode,
                  destinations: selectedPolicy.destinations.map((d) => ({
                    ...d,
                  })),
                  tls_profile_id: selectedPolicy.tls_profile_id,
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
    </div>
  );
}

function PolicyRow({
  policy,
  clients,
  domains,
  total,
  own,
  mark,
  active,
  onClick,
}: {
  policy: Policy;
  clients: number;
  domains: number;
  total?: number;
  own?: boolean;
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
          {own ? "своя" : pluralRu(clients, "клиент", "клиента", "клиентов")}
        </span>
      }
      subtitle={
        policy.mode === "all_web"
          ? "весь веб"
          : pluralRu(domains, "домен", "домена", "доменов")
      }
      aside={
        !own && total ? (
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
  clients: { id: string; name: string }[];
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
            {policy.mode === "all_web"
              ? "весь веб"
              : pluralRu(
                  policy.destinations.length,
                  "домен",
                  "домена",
                  "доменов",
                )}
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
                  ? "Сначала переназначьте клиентов"
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
            <span>Используют:</span>
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
              Изменения затронут{" "}
              {pluralRu(clients.length, "клиента", "клиентов", "клиентов")}:{" "}
              {clients.map((c) => c.name).join(", ")}.
            </div>
          </Callout>
        )}
        <Card>
          <CardHeader>
            <CardTitle className="flex-1 min-w-0">Режим</CardTitle>
          </CardHeader>
          <CardContent>
            <RadioGroup
              value={policy.mode}
              onValueChange={async (v) => {
                if (!editable || v === policy.mode) return;
                const res = await onCommit(
                  setPolicyMode(snap, policy.id, v as Policy["mode"]),
                );
                if (!res.ok && res.code !== "revision_conflict")
                  onToast(res.message ?? "Ошибка");
              }}
              className="grid grid-cols-1 gap-2 min-[1180px]:grid-cols-2"
            >
              <ModeCard
                value="allowlist"
                active={policy.mode === "allowlist"}
                title="Список доменов"
                hint="Только перечисленные сайты"
                disabled={!editable}
              />
              <ModeCard
                value="all_web"
                active={policy.mode === "all_web"}
                title="Весь веб"
                hint="Кроме запрещённого для всех"
                disabled={!editable}
              />
            </RadioGroup>
          </CardContent>
        </Card>
        {policy.mode === "allowlist" && (
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
        )}
        {policy.mode === "allowlist" && (
          <MissingCard
            policy={policy}
            clients={clients}
            snap={snap}
            request={request}
            editable={editable}
            onCommit={onCommit}
            onToast={onToast}
          />
        )}
        {policy.mode === "all_web" && (
          <Callout tone="info" icon={<Globe aria-hidden />}>
            <div className="text-sm">
              Разрешены любые внешние сайты. Действуют запреты для всех и
              встроенная защита, они описаны в «Настройках».
            </div>
          </Callout>
        )}
      </InspectorBody>
    </>
  );
}

// MissingCard: denied domains of this policy's clients over 24 h that the
// policy does not cover, each with "Добавить".
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
  clients: { id: string; name: string }[];
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
      clients.slice(0, 20).map((c) =>
        request<{ top_destinations: HostCount[] }>(
          `/inspector/clients/${c.id}/stats`,
        )
          .then((st) =>
            missingDomains(st.top_destinations, policy).map((d) => ({
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
  }, [request, ids, policy.destinations]);
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

// Mode option card: RadioGroupItem inside a label styled like .opt.
function ModeCard({
  value,
  active,
  title,
  hint,
  disabled,
}: {
  value: string;
  active: boolean;
  title: string;
  hint: string;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <label
      htmlFor={id}
      className={cn(
        "grid grid-cols-[18px_minmax(0,1fr)] gap-2.5 rounded-lg border px-3 py-2.5 items-start",
        active
          ? "border-muted-foreground/45 bg-hover"
          : "border-border",
        disabled ? "cursor-not-allowed opacity-60" : "cursor-pointer",
      )}
    >
      <RadioGroupItem
        id={id}
        value={value}
        disabled={disabled}
        className="mt-px"
      />
      <span className="min-w-0">
        <div className="font-medium">{title}</div>
        <div className="text-sm text-muted-foreground">{hint}</div>
      </span>
    </label>
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
