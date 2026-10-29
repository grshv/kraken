import { useEffect, useMemo, useState } from "react";
import { Plus, ShieldCheck, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "../shell/EmptyState";
import { DomainEditor, type DomainHits } from "./DomainEditor";
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

// PoliciesSection: list of named allowlists with client/domain counts and a
// detail editor (mode, domains, 24h/7d hits, clients). Delete is allowed only
// when no client references the policy; a manager cannot change a shared policy
// used by foreign clients.
export function PoliciesSection(props: SectionProps) {
  const { request, draft, active, session, onCommit, onToast } = props;
  const [selected, setSelected] = useState<string | null>(null);
  const [hits, setHits] = useState<DomainHits>({});
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");

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
      domains: { domain: string; include_subdomains: boolean; hits_24h: number; hits_7d: number }[];
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

  if (policies.length === 0 && !creating) {
    return (
      <div className="placeholder-section">
        <aside className="list-pane">
          <div className="list-head">
            <div className="row" style={{ justifyContent: "space-between" }}>
              <span className="list-title">Политики</span>
              <Button size="xs" onClick={() => setCreating(true)}>
                <Plus aria-hidden /> Новая
              </Button>
            </div>
          </div>
          <div className="list-body">
            <div className="muted-text small" style={{ padding: "12px 14px" }}>
              Политик пока нет.
            </div>
          </div>
        </aside>
        <section className="inspector-pane">
          <div className="inspector-body">
            <EmptyState icon={<ShieldCheck />} title="Нет политик">
              Создайте первую политику с набором доменов, затем назначьте её клиенту.
            </EmptyState>
          </div>
        </section>
      </div>
    );
  }

  if (creating) {
    return (
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
  }

  const shared = policies.filter((p) => {
    const n = clientsOf(p.id).length;
    return n !== 1;
  });
  const own = policies.filter((p) => clientsOf(p.id).length === 1);

  return (
    <div className="placeholder-section">
      <aside className="list-pane">
        <div className="list-head">
          <div className="row" style={{ justifyContent: "space-between" }}>
            <span className="list-title">Политики</span>
            <Button size="xs" onClick={() => setCreating(true)}>
              <Plus aria-hidden /> Новая
            </Button>
          </div>
          <div className="muted-text small">
            Именованные списки доменов. Клиент ссылается на одну политику.
          </div>
        </div>
        <div className="list-body">
          <div className="list-group-label">Общие</div>
          {shared.map((p) => (
            <PolicyRow
              key={p.id}
              policy={p}
              clients={clientsOf(p.id).length}
              domains={p.destinations.length}
              mark={marks[p.id]}
              active={selected === p.id}
              onClick={() => setSelected(p.id)}
            />
          ))}
          {own.length > 0 && <div className="list-group-label">Свои у клиентов</div>}
          {own.map((p) => (
            <PolicyRow
              key={p.id}
              policy={p}
              clients={clientsOf(p.id).length}
              domains={p.destinations.length}
              mark={marks[p.id]}
              active={selected === p.id}
              onClick={() => setSelected(p.id)}
            />
          ))}
          {policies.length === 0 && (
            <div className="muted-text small" style={{ padding: "12px 14px" }}>
              Политик нет
            </div>
          )}
        </div>
      </aside>
      <section className="inspector-pane">
        {selectedPolicy ? (
          <PolicyDetail
            policy={selectedPolicy}
            snap={snap!}
            clients={clientsOf(selectedPolicy.id)}
            hits={hits}
            mark={marks[selectedPolicy.id]}
            session={session}
            request={request}
            onCommit={onCommit}
            onToast={onToast}
            onDelete={async () => {
              if (clientsOf(selectedPolicy.id).length > 0) {
                onToast("Сначала переназначьте клиентов");
                return;
              }
              const res = await onCommit(deletePolicy(snap!, selectedPolicy.id));
              if (res.ok) {
                setSelected(null);
                onToast("Политика удалена");
              }
            }}
          />
        ) : (
          <div className="inspector-body">
            <EmptyState icon={<ShieldCheck />} title="Выберите политику">
              Выберите политику слева или создайте новую.
            </EmptyState>
          </div>
        )}
      </section>
    </div>
  );
}

function PolicyRow({
  policy,
  clients,
  domains,
  mark,
  active,
  onClick,
}: {
  policy: Policy;
  clients: number;
  domains: number;
  mark?: Mark;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      className={"list-row" + (active ? " selected" : "")}
      onClick={onClick}
    >
      <span className="ellipsis" style={{ fontWeight: 500 }}>
        {policy.name}
        {mark === "new" && <Badge>новая</Badge>}
        {mark === "changed" && <Badge variant="secondary">изменена</Badge>}
      </span>
      <span className="muted-text small">
        {clients} клиент{clients === 1 ? "" : "ов"}
      </span>
      <span className="muted-text small">
        {policy.mode === "all_web" ? "весь веб" : `${domains} домен.`}
      </span>
    </button>
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
  onCommit,
  onToast,
  onDelete,
}: {
  policy: Policy;
  snap: Snapshot;
  clients: { id: string; name: string }[];
  hits: DomainHits;
  mark?: Mark;
  session: Session;
  request: SectionProps["request"];
  onCommit: (next: Snapshot) => Promise<CommitResult>;
  onToast: (m: string) => void;
  onDelete: () => Promise<void>;
}) {
  const [editingName, setEditingName] = useState(false);
  const [name, setName] = useState(policy.name);
  const scope = managerScope(session);
  // Manager may edit only if every client referencing the policy is his.
  const editable =
    !scope || clients.every((c) => scope.clientIDs.includes(c.id));
  const hasClients = clients.length > 0;
  return (
    <>
      <header className="inspector-head">
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <h1 className="page-title" style={{ margin: 0 }}>
            {policy.name}
          </h1>
          <Badge variant="secondary">
            {policy.mode === "all_web" ? "весь веб" : `${policy.destinations.length} доменов`}
          </Badge>
          {mark === "new" && <Badge>новая, в черновике</Badge>}
          {mark === "changed" && <Badge variant="secondary">изменена</Badge>}
          <span className="grow" />
          <Button
            size="xs"
            variant="ghost"
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
            <Trash2 aria-hidden /> Удалить
          </Button>
        </div>
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <span className="muted-text small">Используют:</span>
          {hasClients ? (
            clients.map((c) => (
              <Badge key={c.id} variant="outline">
                {c.name}
              </Badge>
            ))
          ) : (
            <span className="muted-text small">никто</span>
          )}
        </div>
      </header>
      <div className="inspector-body">
        {clients.length > 1 && (
          <div className="callout callout-warn">
            <ShieldCheck aria-hidden />
            <div className="small">
              Изменения затронут {clients.length} клиентов:{" "}
              {clients.map((c) => c.name).join(", ")}.
            </div>
          </div>
        )}
        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Название</span>
            {editingName ? (
              <div className="row">
                <Input value={name} onChange={(e) => setName(e.target.value)} style={{ width: 240 }} />
                <Button
                  size="xs"
                  onClick={async () => {
                    const res = await onCommit(renamePolicy(snap, policy.id, name.trim() || policy.name));
                    if (res.ok) setEditingName(false);
                  }}
                >
                  Сохранить
                </Button>
              </div>
            ) : (
              <Button size="xs" variant="ghost" onClick={() => setEditingName(true)} disabled={!editable}>
                Переименовать
              </Button>
            )}
          </div>
        </div>
        <div className="card">
          <div className="card-head">
            <span className="card-title grow">Режим</span>
          </div>
          <div className="card-body">
            <div className="opts">
              <ModeOption
                active={policy.mode === "allowlist"}
                title="Список доменов"
                hint="Только перечисленные сайты"
                disabled={!editable}
                onClick={async () => {
                  const res = await onCommit(setPolicyMode(snap, policy.id, "allowlist"));
                  if (!res.ok && res.code !== "revision_conflict") onToast(res.message ?? "Ошибка");
                }}
              />
              <ModeOption
                active={policy.mode === "all_web"}
                title="Весь веб"
                hint="Кроме запрещённого для всех"
                disabled={!editable}
                onClick={async () => {
                  const res = await onCommit(setPolicyMode(snap, policy.id, "all_web"));
                  if (!res.ok && res.code !== "revision_conflict") onToast(res.message ?? "Ошибка");
                }}
              />
            </div>
          </div>
        </div>
        {policy.mode === "allowlist" && (
          <div className="card">
            <div className="card-head">
              <span className="card-title grow">Домены</span>
            </div>
            <div className="card-body">
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
                  if (!res.ok && res.code !== "revision_conflict") onToast(res.message ?? "Ошибка");
                }}
              />
              <div className="row" style={{ marginTop: 10, gap: 6 }}>
                <TemplateButtons
                  request={request}
                  onAdd={async (doms) => {
                    const res = await onCommit(addDomainsToPolicy(snap, policy.id, doms));
                    if (res.ok) onToast("Шаблон добавлен");
                  }}
                />
              </div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}

function ModeOption({
  active,
  title,
  hint,
  disabled,
  onClick,
}: {
  active: boolean;
  title: string;
  hint: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={"opt" + (active ? " selected" : "")}
      onClick={onClick}
      disabled={disabled}
      aria-pressed={active}
    >
      <span className="radio" aria-hidden />
      <span>
        <div style={{ fontWeight: 500 }}>{title}</div>
        <div className="small muted-text">{hint}</div>
      </span>
    </button>
  );
}

function TemplateButtons({
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
      templates: { id: string; name: string; destinations: { domain: string }[] }[];
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
    <section className="inspector-pane">
      <header className="inspector-head">
        <h1 className="page-title" style={{ margin: 0 }}>Новая политика</h1>
      </header>
      <div className="inspector-body">
        <div className="card">
          <div className="card-body">
            <Label>
              Название
              <Input value={newName} onChange={(e) => onName(e.target.value)} placeholder="например, build-runners" />
            </Label>
            <div className="row" style={{ justifyContent: "flex-end", marginTop: 12 }}>
              <Button variant="ghost" onClick={onCancel}>
                Отмена
              </Button>
              <Button onClick={onSubmit}>
                <Plus aria-hidden /> Добавить в черновик
              </Button>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
