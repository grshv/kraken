import { useEffect, useState } from "react";
import {
  Ban,
  Eye,
  Globe,
  Info,
  Lock,
  Network,
  Plus,
  Server,
  ShieldCheck,
  Clock,
  Code,
  FileText,
  List,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import {
  InspectorBody,
  InspectorHeader,
  InspectorPane,
  ListPane,
  ListPaneBody,
  ListPaneHeader,
  ListRow,
} from "@/components/kraken/layout";
import { Callout, KeyValue, SettingRow } from "@/components/kraken/blocks";
import { EmptyState } from "../shell/EmptyState";
import { ErrorState } from "../shell/ErrorState";
import { BlockedDomainEditor } from "./BlockedDomainEditor";
import {
  effectiveSettings,
  patchSettings,
  setBlockedDomains,
  setUpstream,
  setExpertConfig,
} from "./edits";
import {
  formatDuration,
  parseDuration,
  parseNameservers,
  parsePortList,
} from "./units";
import type { SectionProps } from "../sections/sectionProps";
import type { Settings, Snapshot, UpstreamProxy } from "../model";
import type { NavId } from "../shell/nav";

// Certificate (from /certificates): public metadata only.
type Cert = {
  ref: string;
  purpose: "panel" | "proxy_server";
  subject: string;
  issuer: string;
  sans?: string[];
  fingerprint_sha256: string;
  not_after: string;
};

type SetId =
  | "blocked"
  | "protect"
  | "upstream"
  | "privacy"
  | "ports"
  | "certs"
  | "dns"
  | "timeouts"
  | "cache"
  | "logs"
  | "expert";

const SECTIONS: {
  id: SetId;
  icon: typeof Ban;
  label: string;
  count?: number;
}[] = [
  { id: "blocked", icon: Ban, label: "Запрещено для всех" },
  { id: "protect", icon: ShieldCheck, label: "Встроенная защита" },
  { id: "upstream", icon: Network, label: "Вышестоящий прокси" },
  { id: "privacy", icon: Eye, label: "Приватность" },
  { id: "ports", icon: List, label: "Экземпляр и порты" },
  { id: "certs", icon: Lock, label: "Сертификаты TLS" },
  { id: "dns", icon: Globe, label: "DNS" },
  { id: "timeouts", icon: Clock, label: "Таймауты и лимиты" },
  { id: "cache", icon: Server, label: "Кеш в памяти" },
  { id: "logs", icon: FileText, label: "Журналы" },
  { id: "expert", icon: Code, label: "squid.conf вручную" },
];

// SettingsSection: admin-only visual settings. All edits go through onCommit
// (PUT /configuration/draft with expected_version); 409 toasts and reloads;
// per-field errors surface via ApiError.code returned by onCommit.
export function SettingsSection(props: SectionProps) {
  const {
    request,
    draft,
    active,
    session,
    onCommit,
    onToast,
    onNavigate,
    intent,
    onIntentHandled,
  } = props;
  const [active_id, setActiveId] = useState<SetId>("blocked");
  const [instanceName, setInstanceName] = useState<string>("");
  const [certs, setCerts] = useState<Cert[] | null>(null);
  const [expertTemplate, setExpertTemplate] = useState<string>("");
  const [error, setError] = useState<string | null>(null);

  const snap = draft?.snapshot;
  const activeSnap = active;
  const s = snap ? effectiveSettings(snap) : null;

  // Command palette "blocked-domains": switch to the blocked list once.
  useEffect(() => {
    if (intent !== "blocked-domains") return;
    setActiveId("blocked");
    onIntentHandled?.();
  }, [intent, onIntentHandled]);

  useEffect(() => {
    let live = true;
    request<{ instance: { name: string } }>("/status")
      .then((r) => live && setInstanceName(r.instance?.name ?? ""))
      .catch(() => live && setInstanceName(""));
    request<Cert[]>("/certificates")
      .then((r) => live && setCerts(r ?? []))
      .catch(() => live && setCerts([]));
    request<{ expert_config: string }>("/configuration/expert-template")
      .then((r) => live && setExpertTemplate(r.expert_config ?? ""))
      .catch(() => live && setExpertTemplate(""));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request]);

  if (!snap || !s) {
    return (
      <InspectorPane>
        <InspectorBody>
          <ErrorState
            title="Настройки недоступны"
            detail="Черновик не загружен. Вернитесь позже."
            onRetry={() => location.reload()}
          />
        </InspectorBody>
      </InspectorPane>
    );
  }

  const activeSettings = activeSnap?.settings
    ? effectiveSettings(activeSnap)
    : null;
  // settingsDirty reports whether a settings field differs from active.
  const dirty = (field: keyof Settings): boolean => {
    if (!activeSettings) return true;
    const cur = (s as Settings)[field];
    const was = activeSettings[field];
    return JSON.stringify(cur) !== JSON.stringify(was);
  };

  const commit = async (next: Snapshot, ok: string) => {
    setError(null);
    const res = await onCommit(next);
    if (!res.ok) {
      if (res.code !== "revision_conflict") {
        setError(res.message ?? "Ошибка сохранения");
      }
      return res;
    }
    onToast(ok);
    return res;
  };

  const sectionMeta = SECTIONS.find((x) => x.id === active_id)!;

  // Count of blocked domains for the list counter.
  const blockedCount = s.blocked_domains?.length ?? 0;

  return (
    <>
      <ListPane>
        <ListPaneHeader
          title="Настройки"
          description="Общие для всех клиентов. Изменения тоже идут через черновик."
        />
        <ListPaneBody>
          {SECTIONS.map((sec) => (
            <ListRow
              key={sec.id}
              selected={active_id === sec.id}
              onClick={() => setActiveId(sec.id)}
              leading={<sec.icon aria-hidden />}
              title={sec.label}
              meta={
                sec.id === "blocked" && blockedCount ? blockedCount : undefined
              }
            />
          ))}
        </ListPaneBody>
      </ListPane>
      <InspectorPane>
        <InspectorHeader title={sectionMeta.label} />
        <InspectorBody>
          {error && (
            <Callout tone="bad" icon={<Ban aria-hidden />}>
              <span className="text-sm">{error}</span>
            </Callout>
          )}
          {active_id === "blocked" && (
            <BlockedSection
              snap={snap}
              s={s}
              dirty={dirty("blocked_domains")}
              onCommit={commit}
            />
          )}
          {active_id === "protect" && (
            <ProtectSection
              snap={snap}
              s={s}
              dirty={dirty("allow_private_destinations")}
              onCommit={commit}
            />
          )}
          {active_id === "upstream" && (
            <UpstreamSection
              snap={snap}
              s={s}
              dirty={dirty("upstream_proxy")}
              onCommit={commit}
            />
          )}
          {active_id === "privacy" && (
            <PrivacySection
              snap={snap}
              s={s}
              dirty={dirty("hide_client_address")}
              onCommit={commit}
            />
          )}
          {active_id === "ports" && (
            <PortsSection
              snap={snap}
              s={s}
              instanceName={instanceName}
              dirtySafe={dirty("safe_ports")}
              dirtyConnect={dirty("connect_ports")}
              onCommit={commit}
            />
          )}
          {active_id === "certs" && (
            <CertsSection certs={certs} onNavigate={onNavigate} />
          )}
          {active_id === "dns" && (
            <DnsSection
              snap={snap}
              s={s}
              dirtyPositive={dirty("positive_dns_ttl_seconds")}
              dirtyNegative={dirty("negative_dns_ttl_seconds")}
              dirtyDns={dirty("dns_nameservers")}
              onCommit={commit}
            />
          )}
          {active_id === "timeouts" && (
            <TimeoutsSection
              snap={snap}
              s={s}
              onCommit={commit}
              dirty={dirty}
            />
          )}
          {active_id === "cache" && (
            <CacheSection
              snap={snap}
              s={s}
              dirtyEnabled={dirty("cache_enabled")}
              dirtyMem={dirty("cache_memory_mb")}
              onCommit={commit}
            />
          )}
          {active_id === "logs" && (
            <LogsSection
              snap={snap}
              s={s}
              dirty={dirty("log_rotations")}
              onCommit={commit}
            />
          )}
          {active_id === "expert" && (
            <ExpertSection
              snap={snap}
              expertTemplate={expertTemplate}
              dirty={
                snap.configuration_mode !== activeSnap?.configuration_mode ||
                snap.expert_config !== activeSnap?.expert_config
              }
              onCommit={commit}
            />
          )}
        </InspectorBody>
      </InspectorPane>
    </>
  );
}

// FieldDiffBadge (kept inline) — used to mark a field changed in the draft.
function DiffBadge({ show }: { show: boolean }) {
  return show ? <Badge variant="mod">в черновике</Badge> : null;
}

function BlockedSection({
  snap,
  s,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const blocked = s.blocked_domains ?? [];
  return (
    <>
      <Callout tone="info" icon={<Info aria-hidden />}>
        <span className="text-sm">
          Эти домены закрыты для всех клиентов, включая «весь веб» и squid.conf
          вручную. Проверяются сразу после встроенной защиты.
        </span>
      </Callout>
      <Card>
        {dirty && (
          <CardHeader>
            <CardAction>
              <DiffBadge show />
            </CardAction>
          </CardHeader>
        )}
        <CardContent>
          <BlockedDomainEditor
            destinations={blocked}
            onChange={async (next) => {
              await onCommit(
                setBlockedDomains(snap, next),
                "Список обновлён в черновике",
              );
            }}
          />
        </CardContent>
      </Card>
    </>
  );
}

function ProtectSection({
  snap,
  s,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Loopback, link-local, metadata 169.254.169.254"
          help="Всегда закрыто. Отключить нельзя"
          control={
            <Badge variant="secondary">
              <Lock aria-hidden /> всегда
            </Badge>
          }
        />
        <SettingRow
          title="IPv6-туннели к внутренним адресам"
          help="6to4, Teredo, NAT64 local-use"
          control={
            <Badge variant="secondary">
              <Lock aria-hidden /> всегда
            </Badge>
          }
        />
        <SettingRow
          title="Опасные порты"
          help="SSH, Telnet, SMTP/Submission, POP3, IMAP, Tor"
          control={
            <Badge variant="secondary">
              <Lock aria-hidden /> всегда
            </Badge>
          }
        />
        <SettingRow
          title="Частные сети"
          help="10/8, 172.16/12, 192.168/16, fc00::/7. Включите, если клиентам нужны внутренние репозитории"
          badge={<DiffBadge show={dirty} />}
          control={
            <Switch
              checked={s.allow_private_destinations}
              aria-label="Частные сети"
              onCheckedChange={(v) =>
                onCommit(
                  patchSettings(snap, { allow_private_destinations: v }),
                  v ? "Частные сети разрешены" : "Частные сети закрыты",
                )
              }
            />
          }
        />
      </CardContent>
    </Card>
  );
}

function UpstreamSection({
  snap,
  s,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const up = s.upstream_proxy ?? null;
  const enabled = up !== null;
  const [addr, setAddr] = useState(up?.address ?? "");
  const [port, setPort] = useState<string>(up ? String(up.port) : "");
  const [exclusive, setExclusive] = useState(up?.exclusive ?? false);
  const [addrErr, setAddrErr] = useState<string | null>(null);

  // Keep local inputs in sync when the upstream value changes after commit.
  useEffect(() => {
    setAddr(up?.address ?? "");
    setPort(up ? String(up.port) : "");
    setExclusive(up?.exclusive ?? false);
    setAddrErr(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(up)]);

  function save() {
    setAddrErr(null);
    if (!enabled) {
      onCommit(setUpstream(snap, null), "Вышестоящий прокси выключен");
      return;
    }
    if (!addr.trim()) {
      setAddrErr("Укажите IP-адрес вышестоящего прокси");
      return;
    }
    const portNum = Number(port);
    if (!Number.isInteger(portNum) || portNum < 1 || portNum > 65535) {
      setAddrErr("Порт: целое число 1–65535");
      return;
    }
    const next: UpstreamProxy = {
      address: addr.trim(),
      port: portNum,
      exclusive,
    };
    onCommit(setUpstream(snap, next), "Вышестоящий прокси сохранён");
  }

  return (
    <Card>
      {dirty && (
        <CardHeader>
          <CardAction>
            <DiffBadge show />
          </CardAction>
        </CardHeader>
      )}
      <CardContent>
        <SettingRow
          title="Отправлять трафик через другой прокси"
          help="Например, корпоративный шлюз"
          control={
            <Switch
              checked={enabled}
              aria-label="Вышестоящий прокси"
              onCheckedChange={(v) => {
                if (!v)
                  onCommit(
                    setUpstream(snap, null),
                    "Вышестоящий прокси выключен",
                  );
              }}
            />
          }
        />
        {enabled && (
          <>
            <div className="grid grid-cols-2 gap-3.5 py-3 max-[1180px]:grid-cols-1">
              <Label>
                Адрес
                <Input
                  className="font-mono"
                  value={addr}
                  onChange={(e) => setAddr(e.target.value)}
                  placeholder="10.0.0.10"
                />
                <span className="text-sm text-muted-foreground">Только IP</span>
              </Label>
              <Label>
                Порт
                <Input
                  className="font-mono"
                  value={port}
                  onChange={(e) => setPort(e.target.value)}
                  placeholder="3128"
                />
              </Label>
            </div>
            {addrErr && <div className="text-sm text-bad">{addrErr}</div>}
            <SettingRow
              title="Только через вышестоящий"
              help="Если он недоступен, клиенты получат отказ, а не выйдут напрямую"
              control={
                <Switch
                  checked={exclusive}
                  aria-label="Только через вышестоящий"
                  onCheckedChange={setExclusive}
                />
              }
            />
            <div className="mt-2.5 flex items-center gap-2">
              <Button size="sm" onClick={save}>
                Сохранить
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function PrivacySection({
  snap,
  s,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Скрывать адрес клиента"
          help="Сайты и вышестоящий прокси не увидят X-Forwarded-For и Via"
          badge={<DiffBadge show={dirty} />}
          control={
            <Switch
              checked={!!s.hide_client_address}
              aria-label="Скрывать адрес клиента"
              onCheckedChange={(v) =>
                onCommit(
                  patchSettings(snap, { hide_client_address: v }),
                  v ? "Адрес клиента скрыт" : "Адрес клиента передаётся",
                )
              }
            />
          }
        />
      </CardContent>
    </Card>
  );
}

function PortsSection({
  snap,
  s,
  instanceName,
  dirtySafe,
  dirtyConnect,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  instanceName: string;
  dirtySafe: boolean;
  dirtyConnect: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const [safePorts, setSafePorts] = useState(s.safe_ports.join(", "));
  const [connectPorts, setConnectPorts] = useState(s.connect_ports.join(", "));
  const [safeErr, setSafeErr] = useState<string | null>(null);
  const [connectErr, setConnectErr] = useState<string | null>(null);

  useEffect(() => {
    setSafePorts(s.safe_ports.join(", "));
    setConnectPorts(s.connect_ports.join(", "));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(s.safe_ports), JSON.stringify(s.connect_ports)]);

  function saveSafe() {
    setSafeErr(null);
    const parsed = parsePortList(safePorts);
    if (!parsed) {
      setSafeErr("Порты: целые 1–65535 через запятую");
      return;
    }
    onCommit(
      patchSettings(snap, { safe_ports: parsed }),
      "Порты сайтов сохранены",
    );
  }
  function saveConnect() {
    setConnectErr(null);
    const parsed = parsePortList(connectPorts);
    if (!parsed) {
      setConnectErr("Порты: целые 1–65535 через запятую");
      return;
    }
    onCommit(
      patchSettings(snap, { connect_ports: parsed }),
      "Порты CONNECT сохранены",
    );
  }

  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Имя экземпляра"
          help="Показывается в панели состояния сервера; помогает различать прокси. Задаётся переменной окружения KRAKEN_INSTANCE_NAME"
          control={<Input value={instanceName} readOnly className="w-[200px]" />}
        />
        <SettingRow
          title="Порты сайтов для HTTP"
          help="Через прокси можно открыть только эти порты"
          badge={<DiffBadge show={dirtySafe} />}
          control={
            <Input
              className="font-mono w-[200px]"
              value={safePorts}
              onChange={(e) => setSafePorts(e.target.value)}
            />
          }
        />
        {safeErr && <div className="text-sm text-bad">{safeErr}</div>}
        {dirtySafe && (
          <div className="mt-1.5 flex items-center gap-2">
            <Button size="sm" onClick={saveSafe}>
              Сохранить порты
            </Button>
          </div>
        )}
        <SettingRow
          title="Порты для HTTPS (CONNECT)"
          help="Обычно только 443"
          badge={<DiffBadge show={dirtyConnect} />}
          control={
            <Input
              className="font-mono w-[200px]"
              value={connectPorts}
              onChange={(e) => setConnectPorts(e.target.value)}
            />
          }
        />
        {connectErr && <div className="text-sm text-bad">{connectErr}</div>}
        {dirtyConnect && (
          <div className="mt-1.5 flex items-center gap-2">
            <Button size="sm" onClick={saveConnect}>
              Сохранить порты
            </Button>
          </div>
        )}
        <SettingRow
          title="Порты прокси"
          help="Задаются при установке контейнера"
          control={
            <span className="font-mono text-sm text-muted-foreground">
              {s.http_port} · {s.tls_port} TLS
            </span>
          }
        />
      </CardContent>
    </Card>
  );
}

function CertsSection({
  certs,
  onNavigate,
}: {
  certs: Cert[] | null;
  onNavigate: (id: NavId) => void;
}) {
  if (certs === null) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          Загрузка сертификатов…
        </CardContent>
      </Card>
    );
  }
  if (certs.length === 0) {
    return (
      <EmptyState icon={<Lock />} title="Нет сертификатов">
        Сертификаты не опубликованы или недоступны.
      </EmptyState>
    );
  }
  return (
    <>
      {certs.map((c) => {
        const left = daysUntil(c.not_after);
        const warn = left !== null && left < 30;
        const purpose =
          c.purpose === "panel" ? "Панель управления (HTTPS)" : "Прокси по TLS";
        return (
          <Card key={c.ref}>
            <CardHeader>
              <CardTitle>{purpose}</CardTitle>
              <CardDescription>
                {warn ? (
                  left !== null && left >= 0 ? (
                    <span className="text-warn">
                      Истекает через {pluralDays(left)}
                    </span>
                  ) : (
                    <span className="text-bad">
                      Истёк {pluralDays(-left)} назад
                    </span>
                  )
                ) : left !== null ? (
                  `Действует ещё ${pluralDays(left)}`
                ) : (
                  "Срок действия неизвестен"
                )}
              </CardDescription>
              <CardAction>
                <Badge variant={warn ? "bad" : "ok"}>
                  {warn ? "скоро истечёт" : "в порядке"}
                </Badge>
              </CardAction>
            </CardHeader>
            <CardContent>
              <KeyValue
                items={[
                  [
                    "Кому выдан",
                    <span key="s" className="font-mono text-sm">
                      {c.subject}
                    </span>,
                  ],
                  [
                    "Имена",
                    <span key="n" className="font-mono text-sm">
                      {(c.sans ?? []).join(", ") || c.subject}
                    </span>,
                  ],
                  ["Кем выдан", c.issuer],
                  ["Действует до", formatDate(c.not_after)],
                ]}
              />
            </CardContent>
          </Card>
        );
      })}
      <Callout
        tone="info"
        icon={<Info aria-hidden />}
        actions={
          <Button size="xs" variant="outline" onClick={() => onNavigate("help")}>
            Как заменить
          </Button>
        }
      >
        <span className="text-sm">
          Сертификаты заменяются файлами на сервере и перезапуском контейнера.
          Kraken не выпускает сертификаты сам.
        </span>
      </Callout>
    </>
  );
}

function DnsSection({
  snap,
  s,
  dirtyPositive,
  dirtyNegative,
  dirtyDns,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirtyPositive: boolean;
  dirtyNegative: boolean;
  dirtyDns: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string; message?: string }>;
}) {
  const [positive, setPositive] = useState(
    formatDuration(s.positive_dns_ttl_seconds),
  );
  const [negative, setNegative] = useState(
    formatDuration(s.negative_dns_ttl_seconds),
  );
  const [dns, setDns] = useState(s.dns_nameservers.join(", "));
  const [err, setErr] = useState<string | null>(null);
  const [dnsErr, setDnsErr] = useState<string | null>(null);
  useEffect(() => {
    setPositive(formatDuration(s.positive_dns_ttl_seconds));
    setNegative(formatDuration(s.negative_dns_ttl_seconds));
    setDns(s.dns_nameservers.join(", "));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    s.positive_dns_ttl_seconds,
    s.negative_dns_ttl_seconds,
    JSON.stringify(s.dns_nameservers),
  ]);

  function save() {
    setErr(null);
    const p = parseDuration(positive);
    const n = parseDuration(negative);
    if (p === null || n === null) {
      setErr("Введите длительность, например 60 с или 15 мин");
      return;
    }
    onCommit(
      patchSettings(snap, {
        positive_dns_ttl_seconds: p,
        negative_dns_ttl_seconds: n,
      }),
      "Настройки DNS сохранены",
    );
  }

  // saveDns parses the input with parseNameservers; client-side rejection
  // (null) and server-side rejection (ApiError.code) both surface at the
  // field rather than the top-level error banner.
  async function saveDns() {
    setDnsErr(null);
    const parsed = parseNameservers(dns);
    if (parsed === null) {
      setDnsErr("Каждый сервер — IPv4 или IPv6 без префикса и порта");
      return;
    }
    const res = await onCommit(
      patchSettings(snap, { dns_nameservers: parsed }),
      "DNS-серверы сохранены",
    );
    if (!res.ok && res.code !== "revision_conflict") {
      setDnsErr(res.message ?? res.code ?? "Ошибка");
    }
  }

  return (
    <Card>
      <CardContent>
        <SettingRow
          title="DNS-серверы"
          help="Если пусто, используются DNS контейнера. IPv4 или IPv6 через запятую или пробел"
          badge={<DiffBadge show={dirtyDns} />}
          control={
            <Input
              className="font-mono w-[260px]"
              placeholder="10.0.0.53, 2001:db8::53"
              value={dns}
              onChange={(e) => setDns(e.target.value)}
            />
          }
        />
        {dnsErr && <div className="text-sm text-bad">{dnsErr}</div>}
        {dirtyDns && (
          <div className="mt-1.5 flex items-center gap-2">
            <Button size="sm" onClick={saveDns}>
              Сохранить
            </Button>
          </div>
        )}
        <SettingRow
          title="Кеш успешных ответов"
          help="Чем меньше, тем быстрее видна смена адреса"
          badge={<DiffBadge show={dirtyPositive} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={positive}
              onChange={(e) => setPositive(e.target.value)}
            />
          }
        />
        <SettingRow
          title="Кеш ошибок"
          badge={<DiffBadge show={dirtyNegative} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={negative}
              onChange={(e) => setNegative(e.target.value)}
            />
          }
        />
        {err && <div className="text-sm text-bad">{err}</div>}
        <div className="mt-2 flex items-center gap-2">
          <Button size="sm" onClick={save}>
            Сохранить
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function TimeoutsSection({
  snap,
  s,
  onCommit,
  dirty,
}: {
  snap: Snapshot;
  s: Settings;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
  dirty: (field: keyof Settings) => boolean;
}) {
  const [connect, setConnect] = useState(
    formatDuration(s.connect_timeout_seconds),
  );
  const [read, setRead] = useState(formatDuration(s.read_timeout_seconds));
  const [request, setRequest] = useState(
    formatDuration(s.request_timeout_seconds),
  );
  const [body, setBody] = useState(String(s.max_request_body_mb));
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    setConnect(formatDuration(s.connect_timeout_seconds));
    setRead(formatDuration(s.read_timeout_seconds));
    setRequest(formatDuration(s.request_timeout_seconds));
    setBody(String(s.max_request_body_mb));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    s.connect_timeout_seconds,
    s.read_timeout_seconds,
    s.request_timeout_seconds,
    s.max_request_body_mb,
  ]);

  function save() {
    setErr(null);
    const c = parseDuration(connect);
    const r = parseDuration(read);
    const rq = parseDuration(request);
    const b = Number(body);
    if (c === null || r === null || rq === null) {
      setErr("Введите длительность, например 60 с или 15 мин");
      return;
    }
    if (!Number.isInteger(b) || b < 0 || b > 10240) {
      setErr("Размер тела: целое 0–10240 МБ");
      return;
    }
    onCommit(
      patchSettings(snap, {
        connect_timeout_seconds: c,
        read_timeout_seconds: r,
        request_timeout_seconds: rq,
        max_request_body_mb: b,
      }),
      "Таймауты сохранены",
    );
  }

  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Подключение к сайту"
          badge={<DiffBadge show={dirty("connect_timeout_seconds")} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={connect}
              onChange={(e) => setConnect(e.target.value)}
            />
          }
        />
        <SettingRow
          title="Чтение ответа"
          help="Увеличьте для долгих загрузок образов"
          badge={<DiffBadge show={dirty("read_timeout_seconds")} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={read}
              onChange={(e) => setRead(e.target.value)}
            />
          }
        />
        <SettingRow
          title="Ожидание запроса"
          badge={<DiffBadge show={dirty("request_timeout_seconds")} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={request}
              onChange={(e) => setRequest(e.target.value)}
            />
          }
        />
        <SettingRow
          title="Размер тела запроса"
          help="0 означает без ограничения; МБ"
          badge={<DiffBadge show={dirty("max_request_body_mb")} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          }
        />
        {err && <div className="text-sm text-bad">{err}</div>}
        <div className="mt-2 flex items-center gap-2">
          <Button size="sm" onClick={save}>
            Сохранить
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function CacheSection({
  snap,
  s,
  dirtyEnabled,
  dirtyMem,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirtyEnabled: boolean;
  dirtyMem: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const [mem, setMem] = useState(String(s.cache_memory_mb));
  useEffect(() => {
    setMem(String(s.cache_memory_mb));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.cache_memory_mb]);

  function saveMem() {
    const v = Number(mem);
    if (!Number.isInteger(v) || v < 1 || v > 1024) {
      return;
    }
    onCommit(
      patchSettings(snap, { cache_memory_mb: v }),
      "Объём кеша сохранён",
    );
  }

  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Кеш в памяти"
          help="HTTPS не кешируется: туннели проходят насквозь"
          badge={<DiffBadge show={dirtyEnabled} />}
          control={
            <Switch
              checked={s.cache_enabled}
              aria-label="Кеш в памяти"
              onCheckedChange={(v) =>
                onCommit(
                  patchSettings(snap, { cache_enabled: v }),
                  v ? "Кеш включён" : "Кеш выключен",
                )
              }
            />
          }
        />
        <SettingRow
          title="Объём"
          badge={<DiffBadge show={dirtyMem} />}
          control={
            <>
              <Input
                className="font-mono w-[140px]"
                value={mem}
                onChange={(e) => setMem(e.target.value)}
              />
              <span className="text-sm text-muted-foreground">МБ</span>
            </>
          }
        />
        {dirtyMem && (
          <div className="mt-1.5 flex items-center gap-2">
            <Button size="sm" onClick={saveMem}>
              Сохранить
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function LogsSection({
  snap,
  s,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  s: Settings;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const [v, setV] = useState(String(s.log_rotations));
  useEffect(() => {
    setV(String(s.log_rotations));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [s.log_rotations]);
  function save() {
    const n = Number(v);
    if (!Number.isInteger(n) || n < 0 || n > 30) return;
    onCommit(
      patchSettings(snap, { log_rotations: n }),
      "Настройки журналов сохранены",
    );
  }
  return (
    <Card>
      <CardContent>
        <SettingRow
          title="Хранить ротаций access.log"
          help="История запросов в панели хранится отдельно: 30 дней"
          badge={<DiffBadge show={dirty} />}
          control={
            <Input
              className="font-mono w-[140px]"
              value={v}
              onChange={(e) => setV(e.target.value)}
            />
          }
        />
        {dirty && (
          <div className="mt-1.5 flex items-center gap-2">
            <Button size="sm" onClick={save}>
              Сохранить
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ExpertSection({
  snap,
  expertTemplate,
  dirty,
  onCommit,
}: {
  snap: Snapshot;
  expertTemplate: string;
  dirty: boolean;
  onCommit: (
    next: Snapshot,
    ok: string,
  ) => Promise<{ ok: boolean; code?: string }>;
}) {
  const enabled = snap.configuration_mode === "expert";
  const [text, setText] = useState(snap.expert_config ?? "");

  useEffect(() => {
    setText(snap.expert_config ?? "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [snap.expert_config]);

  return (
    <>
      <Callout tone="warn" icon={<Ban aria-hidden />}>
        <span className="text-sm">
          <strong>Аварийный режим.</strong> Заменяет клиентов и политики текстом
          squid.conf. Встроенная защита и «запрещено для всех» остаются.
          Разрешены только директивы из списка, без файлов, helpers и портов.
        </span>
      </Callout>
      <Card>
        {dirty && (
          <CardHeader>
            <CardAction>
              <DiffBadge show />
            </CardAction>
          </CardHeader>
        )}
        <CardContent>
          <SettingRow
            title="Использовать squid.conf вместо клиентов"
            help={`Сейчас: ${enabled ? "включено" : "выключено"}`}
            control={
              <Switch
                checked={enabled}
                aria-label="Использовать squid.conf"
                onCheckedChange={(v) =>
                  onCommit(
                    setExpertConfig(snap, text, v ? "expert" : "structured"),
                    v ? "Аварийный режим включён" : "Возврат к клиентам",
                  )
                }
              />
            }
          />
          <Textarea
            className="mt-3 font-mono"
            rows={8}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="acl office src 10.8.0.0/16&#10;http_access allow office"
          />
          <div className="mt-2 flex items-center gap-2">
            <Button
              size="sm"
              onClick={() =>
                onCommit(
                  setExpertConfig(
                    snap,
                    text,
                    enabled ? "expert" : "structured",
                  ),
                  "squid.conf сохранён",
                )
              }
            >
              Сохранить
            </Button>
            <Button
              size="xs"
              variant="outline"
              onClick={() =>
                onCommit(
                  setExpertConfig(
                    snap,
                    expertTemplate,
                    enabled ? "expert" : "structured",
                  ),
                  "Шаблон вставлен",
                )
              }
            >
              <Plus aria-hidden /> Вставить шаблон
            </Button>
            <span className="grow" />
            <span className="text-sm text-muted-foreground">
              Директивы проверяются при вводе, вся конфигурация проверяется
              Squid при применении
            </span>
          </div>
        </CardContent>
      </Card>
    </>
  );
}

// daysUntil returns whole days from now until the given RFC3339 timestamp,
// or null when unparseable. Future → positive, past → negative.
function daysUntil(iso: string): number | null {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return null;
  const DAY = 86400000;
  return Math.floor((t - Date.now()) / DAY);
}

function pluralDays(n: number): string {
  const abs = Math.abs(n);
  const mod10 = abs % 10;
  const mod100 = abs % 100;
  const one = mod10 === 1 && mod100 !== 11;
  const few = mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14);
  const word = one ? "день" : few ? "дня" : "дней";
  return `${n} ${word}`;
}

function formatDate(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return iso;
  const d = new Date(t);
  const dd = String(d.getDate()).padStart(2, "0");
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  return `${dd}.${mm}.${d.getFullYear()}`;
}
