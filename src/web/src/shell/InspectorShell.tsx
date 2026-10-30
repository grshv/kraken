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
import { cn } from "cn";
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
import type {
  Draft,
  Operation,
  Preview,
  Session,
  Settings,
  Status,
} from "../model";
import type { Request } from "../api";
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
  badges,
  request,
  settings,
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
  // Counters on rail items, e.g. undescribed sources on Клиенты.
  badges?: Partial<Record<NavId, number>>;
  request: Request;
  // Applied settings: ports for the status popover.
  settings?: Settings;
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
    <div className="flex h-dvh flex-col bg-page">
      <a className="skip-link" href="#main-content">
        К содержимому
      </a>
      <header className="grid h-[52px] flex-none grid-cols-[72px_var(--list-w,300px)_minmax(0,1fr)_minmax(0,520px)_minmax(0,1fr)] items-center border-b bg-card pr-4 max-[1180px]:grid-cols-[72px_min(var(--list-w,300px),260px)_minmax(0,0.4fr)_minmax(0,520px)_minmax(0,1fr)] max-[820px]:grid-cols-[auto_auto_minmax(0,1fr)_auto] max-[820px]:gap-2.5 max-[820px]:px-3.5">
        <div className="flex justify-center">
          <span
            className="grid size-[34px] place-items-center text-foreground"
            aria-hidden
          >
            <KrakenMark title="" className="size-[30px]" />
          </span>
        </div>
        <div className="flex min-w-0 items-center pl-3">
          <span className="whitespace-nowrap text-sm font-semibold uppercase tracking-[0.24em] text-foreground">
            Kraken
          </span>
        </div>
        <div className="max-[820px]:hidden" aria-hidden />
        <button
          type="button"
          className="col-start-4 flex h-[34px] min-w-0 cursor-text items-center gap-2 justify-self-stretch rounded-md border bg-background px-3 text-base text-muted-foreground hover:bg-muted focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring max-[820px]:col-start-3"
          onClick={() => setPaletteOpen(true)}
        >
          <Search aria-hidden className="size-3.5" />
          <span className="grow text-left">
            Клиент, IP, домен или действие…
          </span>
          <kbd className="rounded-[6px] border bg-muted px-[5px] font-mono text-xs text-muted-foreground">
            ⌘K
          </kbd>
        </button>
        <div className="col-start-5 flex justify-end max-[820px]:col-start-4">
          <StatusIndicator
            status={status}
            settings={settings}
            request={request}
            onOpenAudit={handlers.onOpenAudit}
            onOpenSettings={
              items.some((n) => n.id === "settings")
                ? () => onNavigate("settings")
                : undefined
            }
          />
        </div>
      </header>
      {status?.squid.state === "down" && (
        <div
          className="flex flex-none items-center gap-2.5 border-b bg-bad-bg px-4 py-2 text-sm text-bad"
          role="alert"
        >
          <strong>Squid не отвечает.</strong> Трафик клиентов сейчас не
          проходит. Kraken перезапускает Squid с последней рабочей ревизией.
        </div>
      )}
      <div className="grid min-h-0 flex-1 grid-cols-[72px_minmax(0,1fr)]">
        <nav
          className="flex flex-col items-center gap-0.5 overflow-y-auto border-r bg-sidebar py-2"
          aria-label="Разделы"
        >
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
                  badge={badges?.[n.id]}
                  onClick={() => onNavigate(n.id)}
                >
                  <Icon aria-hidden />
                </RailItem>
              );
            })}
          <span className="grow" />
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
                  badge={badges?.[n.id]}
                  onClick={() => onNavigate(n.id)}
                >
                  <Icon aria-hidden />
                </RailItem>
              );
            })}
          <button
            type="button"
            className="flex w-16 flex-col items-center gap-[3px] rounded-md border-0 bg-transparent pb-[6px] pt-[7px] text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring [&_svg]:size-4"
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
          className="flex min-h-0 min-w-0 flex-col bg-background"
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
  badge,
  onClick,
  children,
}: {
  id: NavId;
  label: string;
  active: boolean;
  badge?: number;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={cn(
        "relative flex w-16 flex-col items-center gap-[3px] rounded-md border-0 bg-transparent pb-[6px] pt-[7px] text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring [&_svg]:size-4",
        active && "bg-[var(--accent-soft)] text-accent",
      )}
      aria-current={active ? "page" : undefined}
      data-nav={id}
      onClick={onClick}
    >
      {children}
      <span>{label}</span>
      {!!badge && (
        <span
          className="absolute top-[3px] right-[9px] grid h-[15px] min-w-[15px] place-items-center rounded-lg bg-mod-bg px-[3px] text-xs text-mod-fg"
          aria-label={`${badge} новых`}
        >
          {badge}
        </span>
      )}
    </button>
  );
}

// Re-export Settings2 so the icon map intent is visible at the import site.
export { Settings2 };
