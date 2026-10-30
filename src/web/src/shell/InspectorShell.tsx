import { useCallback, useEffect, useMemo, useState } from "react";
import { Moon, Search, Settings2, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "cn";
import type { NavId, Role } from "./nav";
import { navItems } from "./nav";
import { navIcons } from "./navIcons";
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
  Snapshot,
  Status,
} from "../model";
import type { Request } from "../api";
import type { CommitResult, SectionIntent } from "../sections/sectionProps";
import type { ApplyRun } from "../draft/useApplyFlow";

export type ShellHandlers = {
  onLogout: () => void;
  onOpenAccount: () => void;
  onChangePassword: (current: string, next: string) => Promise<void>;
  onEndOtherSessions: () => void;
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
  onOpen,
  onNavigate,
  handlers,
  toast,
  onToastDismiss,
  confirm,
  onConfirm,
  onConfirmCancel,
  banner,
  active,
  onCommit,
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
  // Opens a section with an entity selected (palette client search).
  onOpen?: (nav: NavId, intent: SectionIntent) => void;
  // Applied settings: ports for the status popover.
  settings?: Settings;
  onNavigate: (id: NavId) => void;
  handlers: ShellHandlers;
  toast: string;
  onToastDismiss: () => void;
  confirm: ConfirmOptions | null;
  onConfirm: () => void;
  onConfirmCancel: () => void;
  // Connection/error strip above the panes; never a pane itself.
  banner?: React.ReactNode;
  active?: Snapshot | null;
  onCommit?: (next: Snapshot) => Promise<CommitResult>;
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
      {/* Brand | search centred on the window | status. Equal side columns keep
          the search in the middle whatever the list width. */}
      <header className="grid h-[52px] flex-none grid-cols-[minmax(0,1fr)_minmax(0,520px)_minmax(0,1fr)] items-center gap-4 border-b bg-card pr-4 max-[820px]:grid-cols-[auto_minmax(0,1fr)_auto] max-[820px]:gap-2.5">
        <div className="flex min-w-0 items-center">
          <span
            className="grid w-[72px] shrink-0 place-items-center text-foreground"
            aria-hidden
          >
            <KrakenMark title="" className="size-[30px]" />
          </span>
          <span className="whitespace-nowrap pl-3 text-sm font-semibold uppercase tracking-[0.24em] text-foreground max-[820px]:hidden">
            Kraken
          </span>
        </div>
        <button
          type="button"
          className="flex h-[34px] min-w-0 cursor-text items-center gap-2 justify-self-stretch rounded-md border bg-background px-3 text-base text-muted-foreground hover:bg-muted focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
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
        <div className="flex justify-end">
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
              const Icon = navIcons[n.id];
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
              const Icon = navIcons[n.id];
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
            onOpenAccount={handlers.onOpenAccount}
            onChangePassword={handlers.onChangePassword}
            onLogout={handlers.onLogout}
            onEndOtherSessions={handlers.onEndOtherSessions}
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
          {banner}
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
        clients={draft?.snapshot.clients ?? []}
        onOpenClient={(id) => onOpen?.("clients", `client:${id}`)}
      />
      <ReviewSheet
        open={reviewOpen}
        onOpenChange={setReviewOpen}
        draft={draft}
        preview={preview}
        status={status}
        run={applyRun}
        request={request}
        onRefreshDraft={handlers.onRefreshDraft}
        active={active}
        onCommit={onCommit}
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
