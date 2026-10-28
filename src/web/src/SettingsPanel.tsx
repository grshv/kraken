import { Panel } from "./components/Panel";
import { Label } from "@/components/ui/label";
import { Alert } from "@/components/ui/alert";
import { Checkbox } from "@/components/ui/checkbox";
import { Disclosure } from "./components/Disclosure";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { SelectField } from "./components/SelectField";
import { SelectItem } from "@/components/ui/select";
import { useEffect, useRef, useState } from "react";
import { defaultSettings } from "./model";
import type { Snapshot, Settings } from "./model";

type Props = {
  snapshot: Snapshot;
  busy: boolean;
  onSave: (s: Snapshot) => Promise<void>;
  onTemplate: () => Promise<string>;
  onPreview: () => Promise<string>;
  onDirtyChange: (dirty: boolean) => void;
};
const tabs = ["Сеть и ACL", "Кеш", "DNS и таймауты", "squid.conf"] as const;
export function SettingsPanel({
  snapshot,
  busy,
  onSave,
  onTemplate,
  onPreview,
  onDirtyChange,
}: Props) {
  const [settings, setSettings] = useState<Settings>(
    structuredClone(snapshot.settings || defaultSettings),
  );
  const [mode, setMode] = useState(snapshot.configuration_mode || "structured");
  const [expert, setExpert] = useState(snapshot.expert_config || "");
  const [tab, setTab] = useState<(typeof tabs)[number]>("Сеть и ACL");
  const dirty =
    JSON.stringify({ settings, mode, expert }) !==
    JSON.stringify({
      settings: snapshot.settings || defaultSettings,
      mode: snapshot.configuration_mode || "structured",
      expert: snapshot.expert_config || "",
    });
  const [error, setError] = useState("");
  const [fullConfig, setFullConfig] = useState("");
  const [configError, setConfigError] = useState("");
  const [configBusy, setConfigBusy] = useState(false);
  const [templateBusy, setTemplateBusy] = useState(false);
  const previewRequest = useRef(onPreview);
  previewRequest.current = onPreview;
  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(() => {
    if (!dirty) return;
    const beforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [dirty]);
  useEffect(() => {
    if (tab !== "squid.conf") return;
    let active = true;
    setConfigBusy(true);
    setConfigError("");
    previewRequest
      .current()
      .then((config) => {
        if (active) setFullConfig(config);
      })
      .catch((e) => {
        if (active) setConfigError(e.message);
      })
      .finally(() => {
        if (active) setConfigBusy(false);
      });
    return () => {
      active = false;
    };
  }, [tab, snapshot]);
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
    setError("");
  }, [snapshot]);
  // Split update into typed paths so number inputs do not need `as never` to
  // bypass the Settings[K] union. Anything else (boolean arrays, string
  // arrays) continues through the generic update.
  function update<K extends keyof Settings>(key: K, value: Settings[K]) {
    setSettings({ ...settings, [key]: value });
  }
  // NumberKeys extracts only the numeric keys so the input handler does not
  // need a cast. `keyof Settings` alone would widen value to the full union.
  type NumberKeys = {
    [K in keyof Settings]: Settings[K] extends number ? K : never;
  }[keyof Settings];
  function updateNumber(key: NumberKeys, value: number) {
    setSettings({ ...settings, [key]: value });
  }
  function number(
    key: NumberKeys,
    label: string,
    min: number,
    max: number,
    hint?: string,
  ) {
    return (
      <Label>
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
      </Label>
    );
  }
  async function loadTemplate() {
    setTemplateBusy(true);
    try {
      setExpert(await onTemplate());
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setTemplateBusy(false);
    }
  }
  async function changeMode(next: string) {
    if (next === "expert" && !expert.trim()) {
      setTemplateBusy(true);
      try {
        const template = await onTemplate();
        setExpert(template);
        setMode(next as "expert" | "structured");
        setError("");
      } catch (e) {
        setError((e as Error).message);
      } finally {
        setTemplateBusy(false);
      }
    } else {
      setMode(next as "expert" | "structured");
    }
  }
  return (
    <Panel className="section settings">
      <Tabs value={tab} onValueChange={(value) => setTab(value as typeof tab)}>
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
            <Label>
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
            </Label>
            <Label>
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
            </Label>
            {number(
              "max_request_body_mb",
              "Максимальный запрос, MiB",
              0,
              10240,
              "0 — без ограничения. Это лимит загрузки на сервер, не скачивания ответа.",
            )}
            {number("log_rotations", "Количество архивов журналов", 0, 30)}
            <Label className="setting-toggle">
              <Checkbox
                aria-label="Разрешить назначения в частных сетях"

                checked={settings.allow_private_destinations}
                onCheckedChange={(checked) =>
                  update("allow_private_destinations", checked === true)
                }
              />
              Разрешить назначения в частных сетях
              <small>Loopback и metadata остаются закрыты.</small>
            </Label>
          </TabsContent>
          <TabsContent value="Кеш" className="settings-grid">
            <Label className="setting-toggle">
              <Checkbox
                aria-label="Включить кеш HTTP-объектов"

                checked={settings.cache_enabled}
                onCheckedChange={(checked) =>
                  update("cache_enabled", checked === true)
                }
              />
              Включить кеш HTTP-объектов
            </Label>
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
            <Alert className="notice">HTTPS-туннели не кешируются.</Alert>
          </TabsContent>
          <TabsContent value="DNS и таймауты" className="settings-grid">
            <Label>
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
            </Label>
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
            {number(
              "read_timeout_seconds",
              "Ожидание чтения, секунды",
              1,
              86400,
            )}
            {number(
              "request_timeout_seconds",
              "Ожидание запроса, секунды",
              1,
              86400,
            )}
          </TabsContent>
          <TabsContent value="squid.conf" className="expert-panel">
            <Label>
              Режим конфигурации
              <SelectField
                aria-label="Режим конфигурации"
                value={mode}
                disabled={busy || templateBusy}
                onValueChange={(value) => {
                  void changeMode(value);
                }}
              >
                <SelectItem value="structured">
                  Визуальные политики клиентов
                </SelectItem>
                <SelectItem
                  value="expert"
                  disabled={snapshot.clients.some(
                    (c) => c.enabled && c.proxy_username,
                  )}
                >
                  Экспертный squid.conf
                </SelectItem>
              </SelectField>
            </Label>
            {snapshot.clients.some((c) => c.enabled && c.proxy_username) && (
              <Alert className="notice">
                Для экспертного режима сначала отключите клиентов с логином
                прокси.
              </Alert>
            )}
            {mode === "expert" && (
              <>
                <div className="expert-toolbar">
                  <h3>Правила доступа</h3>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={busy || templateBusy}
                    onClick={loadTemplate}
                  >
                    Взять правила из форм
                  </Button>
                </div>
                <Label>
                  Экспертная конфигурация
                  <Textarea
                    className="config-editor"
                    aria-label="Экспертная конфигурация"
                    rows={18}
                    value={expert}
                    spellCheck={false}
                    onChange={(e) => {
                      setExpert(e.target.value);
                    }}
                    disabled={busy || templateBusy}
                  />
                </Label>
              </>
            )}
            {templateBusy && <p role="status">Загрузка правил…</p>}
            <Disclosure
              className="final-config"
              title={<> Итоговый squid.conf </>}
            >
              {dirty && (
                <p className="subtle">
                  Показан сохранённый черновик. Сохраните изменения, чтобы
                  обновить конфиг.
                </p>
              )}
              {configBusy ? (
                <p role="status">Загрузка конфигурации…</p>
              ) : configError ? (
                <Alert variant="destructive" className="error" role="alert">
                  {configError}
                </Alert>
              ) : (
                <pre>{fullConfig}</pre>
              )}
            </Disclosure>
          </TabsContent>

          {error && (
            <Alert variant="destructive" className="error" role="alert">
              {error}
            </Alert>
          )}
          <div className="section-footer">
            <span>
              {dirty
                ? "Есть несохранённые изменения"
                : "Изменения сохраняются в черновик"}
            </span>
            <Button disabled={busy || templateBusy || !dirty} type="submit">
              Сохранить настройки в черновик
            </Button>
          </div>
        </form>
      </Tabs>
    </Panel>
  );
}
