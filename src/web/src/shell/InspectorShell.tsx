import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  BookOpen,
  History,
  KeyRound,
  Moon,
  Search,
  Settings2,
  ShieldCheck,
  SlidersHorizontal,
  Sun,
  Users,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { NavId, Role } from "./nav";
import { navItems } from "./nav";
import { KrakenMark } from "./KrakenMark";
import { PaneResizer } from "./PaneResizer";
import { usePaneWidth } from "./usePaneWidth";
import { StatusIndicator } from "./StatusIndicator";
import { ProfileMenu } from "./ProfileMenu";
import { CommandPalette } from "./CommandPalette";
import { DraftTray } from "../draft/DraftTray";
import { ReviewSheet } from "../draft/ReviewSheet";
import { ConfirmDialog, type ConfirmOptions } from "./ConfirmDialog";
import { Toast } from "./Toast";
import { useTheme } from "../ThemeProvider";
import type { Draft, Operation, Preview, Session, Status } from "../model";
import type { ApplyRun } from "../draft/useApplyFlow";

const railIcons: Record<NavId, typeof Users> = {
  clients: Users,
  policies: ShieldCheck,
  log: Activity,
  audit: History,
  accounts: KeyRound,
  help: BookOpen,
  settings: SlidersHorizontal,
};

export type ShellHandlers = {
  onLogout: () => void;
  onChangePassword: () => void;
  onRefreshDraft: () => void;
  onDiscardDraft: () => void;
  onOpenAudit: () => void;
  onAction: (id: string) => void;
};

// Inspector shell: header (logo + word + ⌘K + status), left rail (sections +
// theme + avatar), content area (list/detail panes with resizer), bottom
// draft tray, command palette, confirm dialog and toast.
export function InspectorShell({
  session,
  status,
  draft,
  preview,
  operation,
  applyRun,
  navId,
  onNavigate,
  handlers,
  toast,
  onToastDismiss,
  confirm,
  onConfirm,
  onConfirmCancel,
  children,
}: {
  session: Session;
  status: Status | null;
  draft: Draft | null;
  preview: Preview | null;
  operation: Operation | null;
  applyRun: ApplyRun;
  navId: NavId;
  onNavigate: (id: NavId) => void;
  handlers: ShellHandlers;
  toast: string;
  onToastDismiss: () => void;
  confirm: ConfirmOptions | null;
  onConfirm: () => void;
  onConfirmCancel: () => void;
  children: React.ReactNode;
}) {
  const role: Role = session.role === "manager" ? "manager" : "admin";
  const items = useMemo(() => navItems(role), [role]);
  const list = usePaneWidth("list");
  const detail = usePaneWidth("detail");
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);
  const { dark, setTheme } = useTheme();

  // ⌘K / Ctrl+K toggles the palette; Escape closes palette/review/profile.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
        return;
      }
      if (e.key === "Escape") {
        setPaletteOpen(false);
        if (applyRun.state.phase !== "running") setReviewOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [applyRun.state.phase]);

  const openReview = useCallback(() => {
    if (status?.squid.state === "down") {
      // Sheet still opens so the conflict banner explains why apply is blocked.
    }
    setReviewOpen(true);
  }, [status?.squid.state]);

  const act = useCallback(
    (id: string) => {
      if (id === "act:review") {
        openReview();
        return;
      }
      handlers.onAction(id);
    },
    [handlers, openReview],
  );

  return (
    <div className="inspector-shell">
      <a className="skip-link" href="#main-content">
        К содержимому
      </a>
      <header className="app-header">
        <div className="header-logo-cell">
          <span className="brand-logo" aria-hidden>
            <KrakenMark title="" />
          </span>
        </div>
        <div className="header-word-cell">
          <span className="brand-word">Kraken</span>
        </div>
        <div className="header-gap" aria-hidden />
        <button
          type="button"
          className="search-trigger"
          onClick={() => setPaletteOpen(true)}
        >
          <Search aria-hidden />
          <span className="grow">Клиент, IP, домен или действие…</span>
          <kbd className="kbd">⌘K</kbd>
        </button>
        <div className="header-stat-cell">
          <StatusIndicator status={status} onOpenAudit={handlers.onOpenAudit} />
        </div>
      </header>
      {status?.squid.state === "down" && (
        <div className="down-banner" role="alert">
          <strong>Squid не отвечает.</strong> Трафик клиентов сейчас не
          проходит. Kraken перезапускает Squid с последней рабочей ревизией.
        </div>
      )}
      <div className="app-body">
        <nav className="rail" aria-label="Разделы">
          {items
            .filter((n) => n.group === "main")
            .map((n) => {
              const Icon = railIcons[n.id];
              return (
                <RailItem
                  key={n.id}
                  id={n.id}
                  label={n.label}
                  active={navId === n.id}
                  onClick={() => onNavigate(n.id)}
                >
                  <Icon aria-hidden />
                </RailItem>
              );
            })}
          <span className="rail-spacer" />
          {items
            .filter((n) => n.group === "bottom")
            .map((n) => {
              const Icon = railIcons[n.id];
              return (
                <RailItem
                  key={n.id}
                  id={n.id}
                  label={n.label}
                  active={navId === n.id}
                  onClick={() => onNavigate(n.id)}
                >
                  <Icon aria-hidden />
                </RailItem>
              );
            })}
          <button
            type="button"
            className="rail-item"
            aria-label={dark ? "Светлая тема" : "Тёмная тема"}
            aria-pressed={dark}
            onClick={() => setTheme(dark ? "light" : "dark")}
          >
            {dark ? <Sun aria-hidden /> : <Moon aria-hidden />}
            <span>{dark ? "Светлая" : "Тёмная"}</span>
          </button>
          <ProfileMenu
            session={session}
            onChangePassword={handlers.onChangePassword}
            onLogout={handlers.onLogout}
          />
        </nav>
        <div
          className="app-content"
          id="main-content"
          tabIndex={-1}
          style={
            {
              "--list-w": `${list.width}px`,
              "--detail-w": `${detail.width}px`,
            } as React.CSSProperties
          }
        >
          <div className="pane-frame">
            {children}
            <PaneResizer
              kind="list"
              direction="right"
              onDrag={list.onDrag}
              onCommit={list.onCommit}
              onReset={list.reset}
            />
          </div>
        </div>
      </div>
      <DraftTray
        status={status}
        onReview={openReview}
        onDiscard={handlers.onDiscardDraft}
      />
      <CommandPalette
        role={role}
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        onNavigate={onNavigate}
        onAction={act}
      />
      <ReviewSheet
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        draft={draft}
        preview={preview}
        status={status}
        run={applyRun}
        onRefreshDraft={handlers.onRefreshDraft}
      />
      <ConfirmDialog
        options={confirm}
        onConfirm={onConfirm}
        onCancel={onConfirmCancel}
      />
      <Toast message={toast} onDismiss={onToastDismiss} />
      {/* operation is read by useOperationRecovery in App; expose for tests */}
      <input
        type="hidden"
        data-testid="last-operation-status"
        value={operation?.status ?? ""}
      />
    </div>
  );
}

function RailItem({
  id,
  label,
  active,
  onClick,
  children,
}: {
  id: NavId;
  label: string;
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={"rail-item" + (active ? " active" : "")}
      aria-current={active ? "page" : undefined}
      data-nav={id}
      onClick={onClick}
    >
      {children}
      <span>{label}</span>
    </button>
  );
}

// Re-export Settings2 so the icon map intent is visible at the import site.
export { Settings2 };
