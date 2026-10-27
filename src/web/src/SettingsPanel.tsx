import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { NativeSelect } from "@/components/ui/native-select";
import { useEffect, useRef, useState } from "react";
import { defaultSettings } from "./model";
import type { Snapshot, Settings } from "./model";

type Props = {
  snapshot: Snapshot;
  busy: boolean;
  onSave: (s: Snapshot) => Promise<void>;
  onTemplate: () => Promise<string>;
};
const tabs = [
  "Сеть и ACL",
  "Кеш",
  "DNS и таймауты",
  "Экспертный режим",
] as const;
export function SettingsPanel({ snapshot, busy, onSave, onTemplate }: Props) {
  const [settings, setSettings] = useState<Settings>(
    structuredClone(snapshot.settings || defaultSettings),
  );
  const [mode, setMode] = useState(snapshot.configuration_mode || "structured");
  const [expert, setExpert] = useState(snapshot.expert_config || "");
  const [tab, setTab] = useState<(typeof tabs)[number]>("Сеть и ACL");
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  // Last snapshot version we synchronised from. Used to distinguish an external
  // update (server pushed a newer draft) from our own save: remounting the panel
  // via key=draft.version threw away local tab/dirty/error state, which made
  // even benign saves jump the user back to "Сеть и ACL".
  const lastSnapshotRef = useRef(snapshot);
  useEffect(() => {
    if (snapshot === lastSnapshotRef.current) return;
    lastSnapshotRef.current = snapshot;
    setSettings(structuredClone(snapshot.settings || defaultSettings));
    setMode(snapshot.configuration_mode || "structured");
    setExpert(snapshot.expert_config || "");
    setDirty(false);
    setError("");
  }, [snapshot]);
  // Split update into typed paths so number inputs do not need `as never` to
  // bypass the Settings[K] union. Anything else (boolean arrays, string
  // arrays) continues through the generic update.
  function update<K extends keyof Settings>(key: K, value: Settings[K]) {
    setSettings({ ...settings, [key]: value });
    setDirty(true);
  }
  // NumberKeys extracts only the numeric keys so the input handler does not
  // need a cast. `keyof Settings` alone would widen value to the full union.
  type NumberKeys = {
    [K in keyof Settings]: Settings[K] extends number ? K : never;
  }[keyof Settings];
  function updateNumber(key: NumberKeys, value: number) {
    setSettings({ ...settings, [key]: value });
    setDirty(true);
  }
  function number(
    key: NumberKeys,
    label: string,
    min: number,
    max: number,
    hint?: string,
  ) {
    return (
      <label>
        {label}
        <Input
          type="number"
          value={settings[key] as number}
          min={min}
          max={max}
          required
          onChange={(e) => updateNumber(key, Number(e.target.value))}
        />
        {hint && <small>{hint}</small>}
      </label>
    );
  }
  async function loadTemplate() {
    try {
      setExpert(await onTemplate());
      setDirty(true);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    }
  }
  return (
    <Tabs
      value={tab}
      onValueChange={(value) => setTab(value as typeof tab)}
      className="section settings"
    >
      <div className="section-head">
        <div>
          <h2>Настройки Squid</h2>
        </div>
      </div>
      <div className="settings-tabs">
        <TabsList aria-label="Разделы настроек">
          {tabs.map((name) => (
            <TabsTrigger key={name} value={name}>
              {name}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave({
            ...snapshot,
            settings,
            configuration_mode: mode as "structured" | "expert",
            expert_config: expert,
          });
        }}
      >
        <TabsContent value="Сеть и ACL" className="settings-grid">
          {number(
            "http_port",
            "HTTP-порт внутри контейнера",
            1024,
            65535,
            "Изменение публикации порта Docker выполняется отдельно.",
          )}
          {number(
            "tls_port",
            "TLS-порт внутри контейнера",
            1024,
            65535,
            "Активен только при установленном серверном сертификате.",
          )}
          <label>
            Разрешённые порты назначения
            <Input
              value={settings.safe_ports.join(", ")}
              required
              onChange={(e) => {
                const next = e.target.value
                  .split(/[,\s]+/)
                  .filter(Boolean)
                  .map((v) => Number(v))
                  .filter((n) => Number.isFinite(n));
                update("safe_ports", next);
              }}
            />
            <small>Через запятую, например 80, 443, 8080.</small>
          </label>
          <label>
            Порты CONNECT
            <Input
              value={settings.connect_ports.join(", ")}
              required
              onChange={(e) => {
                const next = e.target.value
                  .split(/[,\s]+/)
                  .filter(Boolean)
                  .map((v) => Number(v))
                  .filter((n) => Number.isFinite(n));
                update("connect_ports", next);
              }}
            />
            <small>Из разрешённых портов назначения.</small>
          </label>
          {number(
            "max_request_body_mb",
            "Максимальный запрос, MiB",
            0,
            10240,
            "0 — без ограничения. Это лимит загрузки на сервер, не скачивания ответа.",
          )}
          {number("log_rotations", "Количество архивов журналов", 0, 30)}
          <label className="setting-toggle">
            <Input
              type="checkbox"
              checked={settings.allow_private_destinations}
              onChange={(e) =>
                update("allow_private_destinations", e.target.checked)
              }
            />
            Разрешить назначения в частных сетях
            <small>Loopback и metadata остаются закрыты.</small>
          </label>
        </TabsContent>
        <TabsContent value="Кеш" className="settings-grid">
          <label className="setting-toggle">
            <Input
              type="checkbox"
              checked={settings.cache_enabled}
              onChange={(e) => update("cache_enabled", e.target.checked)}
            />
            Включить кеш HTTP-объектов
          </label>
          {number(
            "cache_memory_mb",
            "Память для кеша, MiB",
            1,
            1024,
            "Лимит кеша, не всего процесса.",
          )}
          {number(
            "maximum_object_kb",
            "Максимальный объект в памяти, KiB",
            1,
            settings.cache_memory_mb * 1024,
          )}
          <p className="notice">HTTPS-туннели не кешируются.</p>
        </TabsContent>
        <TabsContent value="DNS и таймауты" className="settings-grid">
          <label>
            DNS-серверы
            <Textarea
              rows={3}
              value={settings.dns_nameservers.join("\n")}
              onChange={(e) =>
                update(
                  "dns_nameservers",
                  e.target.value.split(/[,\s]+/).filter(Boolean),
                )
              }
            />
            <small>
              IP с новой строки. Пусто — использовать DNS контейнера.
            </small>
          </label>
          {number(
            "positive_dns_ttl_seconds",
            "Положительный DNS TTL, секунды",
            1,
            604800,
          )}
          {number(
            "negative_dns_ttl_seconds",
            "Отрицательный DNS TTL, секунды",
            1,
            settings.positive_dns_ttl_seconds - 1,
          )}
          {number(
            "connect_timeout_seconds",
            "Подключение к серверу, секунды",
            1,
            86400,
          )}
          {number("read_timeout_seconds", "Ожидание чтения, секунды", 1, 86400)}
          {number(
            "request_timeout_seconds",
            "Ожидание запроса, секунды",
            1,
            86400,
          )}
        </TabsContent>
        <TabsContent value="Экспертный режим" className="expert-panel">
          <label>
            Режим конфигурации
            <NativeSelect
              aria-label="Режим конфигурации"
              value={mode}
              onChange={(e) => {
                setMode(e.target.value as "structured" | "expert");
                setDirty(true);
              }}
            >
              <option value="structured">Визуальные политики клиентов</option>
              <option value="expert">Экспертный squid.conf</option>
            </NativeSelect>
          </label>
          <p className="notice">
            Экспертный конфиг заменяет визуальные правила клиентов.
          </p>
          <div className="expert-toolbar">
            <h3>squid.conf: пользовательская секция</h3>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={loadTemplate}
            >
              Загрузить визуальные правила
            </Button>
          </div>
          <label>
            Экспертная конфигурация
            <Textarea
              className="config-editor"
              aria-label="Экспертная конфигурация"
              rows={18}
              value={expert}
              spellCheck={false}
              onChange={(e) => {
                setExpert(e.target.value);
                setDirty(true);
              }}
              placeholder={
                "# ACL, http_access, refresh_pattern, cache_peer, headers…\nacl office src 192.0.2.0/24\nhttp_access allow office"
              }
            />
          </label>
          <small>
            Служебные пути, порты и команды защищены. Полный конфиг доступен в
            изменениях.
          </small>
        </TabsContent>

        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        <div className="section-footer">
          <span>
            {dirty
              ? "Есть несохранённые изменения"
              : "Изменения сохраняются в черновик"}
          </span>
          <Button disabled={busy || !dirty} type="submit">
            Сохранить настройки в черновик
          </Button>
        </div>
      </form>
    </Tabs>
  );
}
