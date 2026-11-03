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
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Separator } from "@/components/ui/separator";
import { Kbd } from "@/components/ui/kbd";
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
import { applyBlockedReason } from "../draft/applyBlock";
import { draftChangeCount } from "../draft/changeCount";
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

// Inspector shell: a full-height 64 px rail with the mark,
// section icons and the profile menu; the workspace header (section path,
// ⌘K, status); the section's resizable panels with banners above them, the draft tray, the command
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
      <SidebarMenuItem key={n.id} className="relative">
        <Tooltip>
          <TooltipTrigger asChild>
            <SidebarMenuButton
              isActive={current}
              className={cn(
                "relative size-10 justify-center rounded-lg border-0 p-0 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-[19px]",
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
                  data-slot="new-events-indicator"
                  className="absolute top-1 right-1 size-2 rounded-full bg-warn"
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
  const currentLabel = items.find((n) => n.id === navId)?.label ?? "";

  return (
    <div className="h-dvh min-w-[1024px] overflow-hidden bg-background text-foreground">
      <a className="skip-link" href="#main">
        К содержимому
      </a>
      <SidebarProvider
        style={{ "--sidebar-width": "64px" } as React.CSSProperties}
        className="h-full min-h-0"
      >
        <Sidebar
          collapsible="none"
          role="navigation"
          aria-label="Разделы"
          className="items-center border-r border-sidebar-border"
        >
          <div
            className="grid h-16 w-full shrink-0 place-items-center"
            title="Kraken"
          >
            <KrakenMark title="" className="size-7 text-foreground" />
          </div>
          <SidebarContent className="w-full items-center pt-3">
            <SidebarMenu className="w-10 gap-2">
              {items.filter((n) => n.group === "main").map(railItem)}
            </SidebarMenu>
          </SidebarContent>
          <SidebarFooter className="items-center gap-3 px-0 pb-4">
            <SidebarMenu className="w-10 gap-2">
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
          <header className="flex h-16 shrink-0 items-center gap-3 border-b border-header-border bg-header px-6 text-header-foreground">
            <Breadcrumb aria-label="Текущий раздел" className="min-w-0">
              <BreadcrumbList className="flex-nowrap gap-3 text-base">
                <BreadcrumbItem className="text-header-muted">
                  Kraken
                </BreadcrumbItem>
                <BreadcrumbSeparator className="text-header-muted/60">
                  /
                </BreadcrumbSeparator>
                <BreadcrumbItem className="min-w-0">
                  <BreadcrumbPage className="truncate">
                    {currentLabel}
                  </BreadcrumbPage>
                </BreadcrumbItem>
              </BreadcrumbList>
            </Breadcrumb>
            <span className="grow" />
            <Button
              variant="ghost"
              size="sm"
              className="gap-2 text-header-muted"
              onClick={() => setPaletteOpen(true)}
              aria-label="Клиент, IP, домен или действие… ⌘K"
            >
              <Search aria-hidden />
              Поиск
              <Kbd className="ml-2">⌘K</Kbd>
            </Button>
            <Separator orientation="vertical" className="mx-1 !h-4" />
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
          </header>
          {status?.squid.state === "down" && (
            <Callout
              tone="bad"
              className="rounded-none border-x-0 border-t-0 px-6 py-3 text-sm"
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
            changes={draftChangeCount(active, draft?.snapshot)}
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
        applyBlockedReason={applyBlockedReason(draft)}
        canPreview={role === "admin"}
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
