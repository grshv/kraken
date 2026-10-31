import { Tooltip, TooltipTrigger, TooltipContent } from "../ui/tooltip";
import * as React from "react";
import { cn } from "cn";
import { ResizablePanel, ResizableHandle } from "../ui/resizable";
import { Button } from "../ui/button";
import { ScrollArea } from "../ui/scroll-area";

// Shared section wrappers compose shadcn Resizable panels.

export function ListPane({
  className,
  ...props
}: React.ComponentProps<"aside">) {
  return (
    <>
      <ResizablePanel
        id="list"
        defaultSize={300}
        minSize={220}
        maxSize={440}
        groupResizeBehavior="preserve-pixel-size"
      >
        <aside
          data-slot="list-pane"
          className={cn(
            "flex h-full min-h-0 min-w-0 flex-col overflow-hidden bg-list text-list-foreground",
            className,
          )}
          {...props}
        />
      </ResizablePanel>
      <ResizableHandle
        aria-label="Ширина списка"
        className="focus-visible:ring-0 focus-visible:ring-offset-0"
      />
    </>
  );
}

// Title row with an optional action (e.g. "+ Новый"); children go below it
// (search input, filter toggles).
export function ListPaneHeader({
  title,
  description,
  action,
  children,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2 border-b px-3 pt-3 pb-2.5">
      <div className="flex min-h-[26px] items-center gap-2">
        <span className="grow text-lg font-semibold">{title}</span>
        {action}
      </div>
      {description && (
        <div className="text-sm text-muted-foreground">{description}</div>
      )}
      {children}
    </div>
  );
}

export function ListPaneBody({
  className,
  children,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <ScrollArea className="min-h-0 flex-1">
      <div className={className} {...props}>
        {children}
      </div>
    </ScrollArea>
  );
}

export function ListGroupLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="px-3.5 pt-2.5 pb-1 text-sm font-medium text-muted-foreground">
      {children}
    </div>
  );
}

// Two-line list row: title and meta on the first line, subtitle and side value
// on the second. Every slot is optional; `leading` is an icon column (settings).
export function ListRow({
  selected,
  leading,
  title,
  meta,
  subtitle,
  aside,
  className,
  ...props
}: Omit<React.ComponentProps<"button">, "title"> & {
  selected?: boolean;
  leading?: React.ReactNode;
  title: React.ReactNode;
  meta?: React.ReactNode;
  subtitle?: React.ReactNode;
  aside?: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          aria-current={selected ? "true" : undefined}
          data-selected={selected ? "" : undefined}
          className={cn(
            "grid h-auto w-full cursor-pointer items-center gap-x-2 gap-y-px rounded-none border-b px-3.5 py-2 text-left text-base font-normal whitespace-normal hover:bg-hover data-[selected]:bg-sel data-[selected]:text-sel-foreground data-[selected]:[&_.text-muted-foreground]:text-muted-on-sel",
            leading
              ? "grid-cols-[auto_minmax(0,1fr)_auto]"
              : "grid-cols-[minmax(0,1fr)_auto]",
            className,
          )}
          {...props}
        >
          {leading && (
            <span
              className={cn(
                "flex text-muted-foreground [&_svg]:size-3.5",
                (subtitle || aside) && "row-span-2",
              )}
            >
              {leading}
            </span>
          )}
          <span className="min-w-0 truncate">{title}</span>
          <span className="justify-self-end text-sm whitespace-nowrap">
            {meta}
          </span>
          {(subtitle || aside) && (
            <>
              <span className="min-w-0 truncate text-sm text-muted-foreground">
                {subtitle}
              </span>
              <span className="justify-self-end text-sm whitespace-nowrap tabular-nums">
                {aside}
              </span>
            </>
          )}
        </Button>
      </TooltipTrigger>
      <TooltipContent
        side="right"
        align="start"
        sideOffset={8}
        className="max-w-sm text-sm [overflow-wrap:anywhere]"
      >
        <div className="font-medium">{title}</div>
        {subtitle && <div className="mt-1 font-normal">{subtitle}</div>}
      </TooltipContent>
    </Tooltip>
  );
}

export function InspectorPane({
  className,
  ...props
}: React.ComponentProps<"section">) {
  return (
    <ResizablePanel id="inspector" minSize={500}>
      <section
        data-slot="inspector-pane"
        className={cn(
          "flex h-full min-h-0 min-w-0 flex-1 flex-col bg-main",
          className,
        )}
        {...props}
      />
    </ResizablePanel>
  );
}

// Page header: optional breadcrumbs, title with actions on the right, meta line.
// Tabs (shadcn Tabs, variant="line") go right after it.
export function InspectorHeader({
  crumbs,
  title,
  status,
  actions,
  meta,
}: {
  crumbs?: React.ReactNode;
  title: React.ReactNode;
  status?: React.ReactNode;
  actions?: React.ReactNode;
  meta?: React.ReactNode;
}) {
  return (
    <header className="flex flex-none flex-col gap-1.5 px-5 pt-3.5 pb-2.5">
      {crumbs}
      <div className="flex min-w-0 items-center gap-2">
        <h1 className="m-0 truncate text-xl font-semibold tracking-tight">
          {title}
        </h1>
        {status}
        <span className="grow" />
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>
      {meta && (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
          {meta}
        </div>
      )}
    </header>
  );
}

export function InspectorBody({
  className,
  children,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <ScrollArea className="min-h-0 min-w-0 flex-1">
      <div
        className={cn(
          "@container flex min-h-0 min-w-0 flex-col gap-3.5 px-5 pt-4 pb-5 [&>*]:shrink-0",
          className,
        )}
        {...props}
      >
        {children}
      </div>
    </ScrollArea>
  );
}

// Right-hand shadcn panel in the requests panel group.
export function DetailPane({
  className,
  children,
  ...props
}: React.ComponentProps<"aside">) {
  return (
    <ResizablePanel
      id="request-detail"
      defaultSize={340}
      minSize={280}
      maxSize={520}
      groupResizeBehavior="preserve-pixel-size"
    >
      <ScrollArea className="h-full">
        <aside
          data-slot="detail-pane"
          className={cn(
            "flex min-h-full flex-col gap-3 bg-card p-4",
            className,
          )}
          {...props}
        >
          {children}
        </aside>
      </ScrollArea>
    </ResizablePanel>
  );
}
