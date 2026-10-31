import { useCallback, useEffect, useMemo, useState } from "react";
import { Search } from "lucide-react";
import { toast as showToast } from "sonner";
import { useDefaultLayout } from "react-resizable-panels";
import { Button } from "@/components/ui/button";
import { ResizablePanelGroup } from "@/components/ui/resizable";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
} from "@/components/ui/sidebar";
import { Toaster } from "@/components/ui/sonner";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Callout } from "@/components/kraken/blocks";
import { cn } from "cn";
import type { NavId, NavItem, Role } from "./nav";
import { navItems } from "./nav";
import { navIcons } from "./navIcons";
import { KrakenMark } from "./KrakenMark";
import { StatusIndicator } from "./StatusIndicator";
import { ProfileMenu } from "./ProfileMenu";
import { CommandPalette } from "./CommandPalette";
import { DraftTray } from "../draft/DraftTray";
import { ReviewSheet } from "../draft/ReviewSheet";
import { ConfirmDialog, type ConfirmOptions } from "./ConfirmDialog";
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

// Inspector shell from the prototype: header (logo, ⌘K, status), a 48 px rail
// of section icons with the profile menu at the bottom, the section's
// resizable panels with banners above them, the draft tray, the command
// palette, the review sheet, the confirm dialog and toasts.
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
  // Counters on rail items, e.g. new notifications on Клиенты.
  badges?: Partial<Record<NavId, number>>;
  request: Request;
  // Opens a section with an entity selected (palette search).
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
  const layout = useDefaultLayout({ id: "kraken-main-panels" });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);

  // ⌘K / Ctrl+K toggles the palette; Escape closes palette/review.
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

  // The App keeps one message; sonner shows it and the App forgets it.
  useEffect(() => {
    if (!toast) return;
    showToast(toast, { id: "kraken-notice" });
    onToastDismiss();
  }, [toast, onToastDismiss]);

  const act = useCallback(
    (id: string) => {
      if (id === "act:review") {
        setReviewOpen(true);
        return;
      }
      handlers.onAction(id);
    },
    [handlers],
  );

  const railItem = (n: NavItem) => {
    const Icon = navIcons[n.id];
    const current = navId === n.id;
    const badge = badges?.[n.id];
    return (
      <SidebarMenuItem
        key={n.id}
        className={
          current
            ? "before:absolute before:top-2 before:bottom-2 before:-left-1.5 before:w-0.5 before:rounded-full before:bg-primary"
            : undefined
        }
      >
        <Tooltip>
          <TooltipTrigger asChild>
            <SidebarMenuButton
              isActive={current}
              className={cn(
                "relative size-9 shrink-0 justify-center rounded-md border-0 p-0 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-[18px]",
                current
                  ? "bg-sidebar-accent text-sidebar-accent-foreground"
                  : "bg-transparent text-sidebar-foreground",
              )}
              aria-label={badge ? `${n.label}, ${badge} новых` : n.label}
              aria-current={current ? "page" : undefined}
              data-nav={n.id}
              onClick={() => onNavigate(n.id)}
            >
              <Icon aria-hidden />
              {!!badge && (
                <span
                  className="absolute top-0.5 right-0.5 size-2 rounded-full bg-mod-fg"
                  aria-hidden
                />
              )}
            </SidebarMenuButton>
          </TooltipTrigger>
          <TooltipContent side="right">
            {n.label}
            {!!badge && ` · ${badge} новых`}
          </TooltipContent>
        </Tooltip>
      </SidebarMenuItem>
    );
  };

  return (
    <div className="flex h-dvh min-w-[1024px] flex-col overflow-hidden bg-background text-foreground">
      <a className="skip-link" href="#main">
        К содержимому
      </a>
      {/* Brand | search centred on the window | status. Equal side columns keep
          the search in the middle whatever the list width. */}
      <header className="grid h-[52px] shrink-0 grid-cols-[minmax(0,1fr)_minmax(0,520px)_minmax(0,1fr)] items-center gap-4 border-b border-header-border bg-header pr-4 text-header-foreground">
        <div className="flex min-w-0 items-center">
          <span className="grid w-12 shrink-0 place-items-center" aria-hidden>
            <KrakenMark title="" className="size-[26px]" />
          </span>
          <strong className="pl-2 text-sm tracking-[.24em]">KRAKEN</strong>
        </div>
        <Button
          variant="outline"
          className="h-[34px] min-w-0 justify-start border-transparent bg-header-field font-normal text-header-muted shadow-none hover:bg-header-field hover:text-header-foreground"
          onClick={() => setPaletteOpen(true)}
        >
          <Search aria-hidden />
          Клиент, IP, домен или действие…
          <span className="ml-auto text-xs">⌘K</span>
        </Button>
        <div className="flex items-center justify-end">
          <StatusIndicator
            status={status}
            settings={settings}
            request={request}
            onOpenAudit={handlers.onOpenAudit}
            onOpenSettings={
              items.some((n) => n.id === "settings")
                ? () =>
                    onOpen
                      ? onOpen("settings", "setting:ports")
                      : onNavigate("settings")
                : undefined
            }
          />
        </div>
      </header>
      <SidebarProvider
        style={{ "--sidebar-width": "48px" } as React.CSSProperties}
        className="min-h-0 flex-1"
      >
        <Sidebar
          collapsible="none"
          role="navigation"
          aria-label="Разделы"
          className="items-center gap-0.5 border-r py-2"
        >
          <SidebarContent className="w-full items-center">
            <SidebarMenu className="w-9 gap-1">
              {items.filter((n) => n.group === "main").map(railItem)}
            </SidebarMenu>
          </SidebarContent>
          <SidebarFooter className="items-center p-0">
            <SidebarMenu className="w-9 gap-1">
              {items.filter((n) => n.group === "bottom").map(railItem)}
            </SidebarMenu>
            <ProfileMenu
              session={session}
              onOpenAccount={handlers.onOpenAccount}
              onChangePassword={handlers.onChangePassword}
              onLogout={handlers.onLogout}
              onEndOtherSessions={handlers.onEndOtherSessions}
            />
          </SidebarFooter>
        </Sidebar>
        <SidebarInset id="main" tabIndex={-1} className="min-h-0 min-w-0">
          {status?.squid.state === "down" && (
            <Callout
              tone="bad"
              className="rounded-none border-x-0 border-t-0 px-4 py-2 text-sm"
            >
              Squid не отвечает. Трафик клиентов сейчас не проходит. Kraken
              перезапускает Squid с последней рабочей ревизией.
            </Callout>
          )}
          {banner}
          <ResizablePanelGroup
            orientation="horizontal"
            id="kraken-main"
            defaultLayout={layout.defaultLayout}
            onLayoutChanged={layout.onLayoutChanged}
            className="min-h-0 flex-1"
          >
            {children}
          </ResizablePanelGroup>
          <DraftTray
            status={status}
            onReview={() => setReviewOpen(true)}
            onDiscard={handlers.onDiscardDraft}
          />
        </SidebarInset>
      </SidebarProvider>
      <CommandPalette
        role={role}
        open={paletteOpen}
        onOpenChange={setPaletteOpen}
        onNavigate={onNavigate}
        onAction={act}
        snapshot={draft?.snapshot ?? null}
        request={request}
        onOpen={onOpen}
        onCommit={onCommit}
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
        onOpenAudit={() => {
          setReviewOpen(false);
          handlers.onOpenAudit();
        }}
      />
      <ConfirmDialog
        options={confirm}
        onConfirm={onConfirm}
        onCancel={onConfirmCancel}
      />
      <Toaster offset={{ bottom: 72 }} />
      {/* operation is read by useOperationRecovery in App; expose for tests */}
      <input
        type="hidden"
        data-testid="last-operation-status"
        value={operation?.status ?? ""}
      />
    </div>
  );
}
