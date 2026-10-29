import { useEffect, useMemo, useRef, useState } from "react";
import { Search } from "lucide-react";
import type { NavId } from "./nav";
import { navItems } from "./nav";
import type { Role } from "./nav";
import { paletteIntent } from "./paletteIntent";

type Action = {
  id: string;
  label: string;
  group: string;
  hint?: string;
  run: () => void;
};

// ⌘K / Ctrl+K command palette. Actions: navigation to every visible section,
// plus new client, block domain for all, review & apply, and new proxy login.
// Actions whose paletteIntent is null for the role (e.g. block-domain for
// manager) are hidden, so the palette never offers a dead action.
export function CommandPalette({
  role,
  open,
  onOpenChange,
  onNavigate,
  onAction,
}: {
  role: Role;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onNavigate: (id: NavId) => void;
  onAction: (id: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const actions = useMemo<Action[]>(() => {
    const nav: Action[] = navItems(role).map((n) => ({
      id: `nav:${n.id}`,
      label: n.label,
      group: "Разделы",
      run: () => onNavigate(n.id),
    }));
    const stubs: Action[] = [
      {
        id: "act:new-client",
        label: "Новый клиент",
        group: "Действия",
        hint: "Создать источник доступа",
        run: () => onAction("act:new-client"),
      },
      {
        id: "act:block-domain",
        label: "Запретить домен для всех",
        group: "Действия",
        run: () => onAction("act:block-domain"),
      },
      {
        id: "act:review",
        label: "Проверить и применить черновик",
        group: "Действия",
        hint: "⌘↵",
        run: () => onAction("act:review"),
      },
      {
        id: "act:new-login",
        label: "Новый логин прокси",
        group: "Действия",
        run: () => onAction("act:new-login"),
      },
    ];
    return [...stubs, ...nav].filter((a) => paletteIntent(a.id, role) !== null);
  }, [role, onNavigate, onAction]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return actions;
    return actions.filter((a) => a.label.toLowerCase().includes(q));
  }, [actions, query]);

  useEffect(() => {
    if (open) {
      setQuery("");
      setActive(0);
      // focus after render
      const t = setTimeout(() => inputRef.current?.focus(), 0);
      return () => clearTimeout(t);
    }
  }, [open]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  // Reset active index when the filtered list shrinks below it.
  useEffect(() => {
    if (active >= filtered.length) setActive(0);
  }, [filtered.length, active]);

  if (!open) return null;

  function choose(a: Action) {
    onOpenChange(false);
    a.run();
  }

  function onKey(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onOpenChange(false);
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => (i + 1) % Math.max(1, filtered.length));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(
        (i) =>
          (i - 1 + Math.max(1, filtered.length)) % Math.max(1, filtered.length),
      );
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const a = filtered[active];
      if (a) choose(a);
    }
  }

  const groups = Array.from(new Set(filtered.map((a) => a.group)));

  return (
    <div className="palette-overlay" onClick={() => onOpenChange(false)}>
      <div
        role="dialog"
        aria-label="Команды"
        className="palette"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="palette-input">
          <Search aria-hidden />
          <input
            ref={inputRef}
            aria-label="Команды"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKey}
            placeholder="Клиент, IP, домен или действие…"
          />
        </div>
        <div className="palette-list">
          {groups.map((g) => (
            <div key={g}>
              <div className="palette-group">{g}</div>
              {filtered
                .filter((a) => a.group === g)
                .map((a) => {
                  const idx = filtered.indexOf(a);
                  return (
                    <button
                      key={a.id}
                      type="button"
                      className={
                        "palette-option" + (idx === active ? " active" : "")
                      }
                      onMouseEnter={() => setActive(idx)}
                      onClick={() => choose(a)}
                    >
                      <span className="grow">{a.label}</span>
                      {a.hint && <kbd className="kbd">{a.hint}</kbd>}
                    </button>
                  );
                })}
            </div>
          ))}
          {filtered.length === 0 && (
            <div className="palette-empty">Ничего не найдено</div>
          )}
        </div>
      </div>
    </div>
  );
}
