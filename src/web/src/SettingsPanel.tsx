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
  "Возможности",
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
  const tabRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  function update<K extends keyof Settings>(key: K, value: Settings[K]) {
    setSettings({ ...settings, [key]: value });
    setDirty(true);
  }
  function number(
    key: keyof Settings,
    label: string,
    min: number,
    max: number,
    hint?: string,
  ) {
    return (
      <label>
        {label}
        <input
          type="number"
          value={settings[key] as number}
          min={min}
          max={max}
          required
          onChange={(e) => update(key, Number(e.target.value) as never)}
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
  function cycleTab(direction: 1 | -1) {
    const i = tabs.indexOf(tab);
    const next = tabs[(i + direction + tabs.length) % tabs.length];
    setTab(next);
    tabRefs.current[next]?.focus();
  }
  function onTabKeyDown(e: React.KeyboardEvent<HTMLButtonElement>) {
    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      cycleTab(1);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      cycleTab(-1);
    } else if (e.key === "Home") {
      e.preventDefault();
      setTab(tabs[0]);
      tabRefs.current[tabs[0]]?.focus();
    } else if (e.key === "End") {
      e.preventDefault();
      setTab(tabs[tabs.length - 1]);
      tabRefs.current[tabs[tabs.length - 1]]?.focus();
    }
  }
  return (
    <section className="section settings">
      <div className="section-head">
        <div>
          <h2>Настройки Squid</h2>
          <p>Параметры входят в ту же ревизию, что и правила доступа.</p>
        </div>
        <span className="pill neutral">Squid 6.13 · GnuTLS</span>
      </div>
      <div
        className="settings-tabs"
        role="tablist"
        aria-label="Разделы настроек"
      >
        {tabs.map((name) => {
          const selected = tab === name;
          return (
            <button
              type="button"
              role="tab"
              id={`settings-tab-${name}`}
              aria-selected={selected}
              aria-controls={`settings-panel-${name}`}
              tabIndex={selected ? 0 : -1}
              key={name}
              ref={(el) => {
                tabRefs.current[name] = el;
              }}
              onClick={() => setTab(name)}
              onKeyDown={onTabKeyDown}
              className={selected ? "selected" : ""}
            >
              {name}
            </button>
          );
        })}
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
        {tab === "Сеть и ACL" && (
          <div
            className="settings-grid"
            role="tabpanel"
            id="settings-panel-Сеть и ACL"
            aria-labelledby="settings-tab-Сеть и ACL"
          >
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
              <input
                value={settings.safe_ports.join(", ")}
                required
                onChange={(e) => {
                  const next = e.target.value
                    .split(/[,\s]+/)
                    .filter(Boolean)
                    .map((v) => Number(v))
                    .filter((n) => Number.isFinite(n));
                  update("safe_ports", next as never);
                }}
              />
              <small>
                Например: 80, 443, 8080. Запрещены: 22, 23, 25, 110, 143, 465,
                587, 993, 995, 9050, 9051.
              </small>
            </label>
            <label>
              Порты CONNECT
              <input
                value={settings.connect_ports.join(", ")}
                required
                onChange={(e) => {
                  const next = e.target.value
                    .split(/[,\s]+/)
                    .filter(Boolean)
                    .map((v) => Number(v))
                    .filter((n) => Number.isFinite(n));
                  update("connect_ports", next as never);
                }}
              />
              <small>
                Подмножество разрешённых портов. Туннель может передавать
                произвольный TCP-протокол.
              </small>
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
              <input
                type="checkbox"
                checked={settings.allow_private_destinations}
                onChange={(e) =>
                  update("allow_private_destinations", e.target.checked)
                }
              />
              Разрешить назначения в частных сетях
              <small>
                Нужно, например, для внутренних сервисов. Loopback и
                link-local/metadata остаются закрыты.
              </small>
            </label>
          </div>
        )}
        {tab === "Кеш" && (
          <div
            className="settings-grid"
            role="tabpanel"
            id="settings-panel-Кеш"
            aria-labelledby="settings-tab-Кеш"
          >
            <label className="setting-toggle">
              <input
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
              "cache_mem не ограничивает суммарную память процесса. Учитывайте лимит контейнера.",
            )}
            {number(
              "maximum_object_kb",
              "Максимальный объект в памяти, KiB",
              1,
              settings.cache_memory_mb * 1024,
            )}
            <p className="notice">
              Обычный HTTPS CONNECT не кешируется. HTTP Cache-Control и другие
              правила ответа сохраняют действие. Дисковый cache_dir требует
              отдельной подготовки хранилища.
            </p>
          </div>
        )}
        {tab === "DNS и таймауты" && (
          <div
            className="settings-grid"
            role="tabpanel"
            id="settings-panel-DNS и таймауты"
            aria-labelledby="settings-tab-DNS и таймауты"
          >
            <label>
              DNS-серверы
              <textarea
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
          </div>
        )}
        {tab === "Экспертный режим" && (
          <div
            className="expert-panel"
            role="tabpanel"
            id="settings-panel-Экспертный режим"
            aria-labelledby="settings-tab-Экспертный режим"
          >
            <label>
              Режим конфигурации
              <select
                aria-label="Режим конфигурации"
                value={mode}
                onChange={(e) => {
                  setMode(e.target.value as "structured" | "expert");
                  setDirty(true);
                }}
              >
                <option value="structured">Визуальные политики клиентов</option>
                <option value="expert">Экспертный squid.conf</option>
              </select>
            </label>
            <p className="notice">
              В экспертном режиме блок правил клиентов заменяется текстом ниже.
              Визуальные политики сохраняются для возврата, но не действуют.
              Визуальные настройки остаются исходными значениями; экспертные
              директивы могут их переопределять. Для правил cache визуальный
              переключатель задаёт завершающее правило.
            </p>
            <div className="expert-toolbar">
              <h3>squid.conf: пользовательская секция</h3>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={loadTemplate}
              >
                Загрузить визуальные правила
              </button>
            </div>
            <label>
              Экспертная конфигурация
              <textarea
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
              Служебные PID/log paths, порты, namespace sc_, includes и
              исполняемые helpers управляются приложением. Произвольные команды
              и смена runtime-путей здесь запрещены. Итоговый полный конфиг
              доступен в просмотре ревизии; неизвестные директивы проверяет
              настоящий Squid.
            </small>
          </div>
        )}
        {tab === "Возможности" && (
          <div
            className="capability-list"
            role="tabpanel"
            id="settings-panel-Возможности"
            aria-labelledby="settings-tab-Возможности"
          >
            <h3>Что доступно в этой сборке</h3>
            <dl>
              <dt>Визуально</dt>
              <dd>
                Клиенты, сети и группы, allowlist, порты, память кеша, DNS,
                таймауты, лимит запроса и ротация логов.
              </dd>
              <dt>Через squid.conf</dt>
              <dd>
                Пользовательские ACL, порядок http_access, методы,
                refresh_pattern, cache_peer, правила заголовков и другие
                директивы, поддержанные установленным Squid и защитным контуром
                редактора.
              </dd>
              <dt>Требует подготовки</dt>
              <dd>
                Аутентификация клиентов через helpers, внешние ACL и дисковый
                кеш — отдельный профиль с установленными программами/каталогами.
                В текущем редакторе эти пути не открыты.
              </dd>
              <dt>HTTPS сайтов</dt>
              <dd>
                Сквозной CONNECT-туннель с исходным сертификатом сайта. Доступны
                домен и порт, содержимое остаётся закрытым.
              </dd>
            </dl>
          </div>
        )}
        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        <div className="section-footer">
          <span>
            {dirty
              ? "Есть несохранённые изменения формы"
              : "Изменения сохраняются в черновик"}
          </span>
          <button disabled={busy || !dirty} type="submit">
            Сохранить настройки в черновик
          </button>
        </div>
      </form>
    </section>
  );
}
