import * as React from "react";
import { cn } from "cn";

// Section layout from the prototype: a list pane (left, resizable through
// --list-w set by InspectorShell) and an inspector pane (rest). Sections compose
// these instead of styling their own containers.

export function ListPane({
  className,
  ...props
}: React.ComponentProps<"aside">) {
  return (
    <aside
      data-slot="list-pane"
      className={cn(
        "flex min-h-0 w-[var(--list-w,300px)] min-w-0 shrink-0 flex-col border-r",
        className,
      )}
      {...props}
    />
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
      <div className="flex min-h-8 items-center gap-2">
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
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div className={cn("min-h-0 flex-1 overflow-y-auto", className)} {...props} />
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
    <button
      type="button"
      aria-current={selected ? "true" : undefined}
      data-selected={selected ? "" : undefined}
      className={cn(
        "grid w-full cursor-pointer items-center gap-x-2 gap-y-px border-b px-3.5 py-2 text-left text-base outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset data-[selected]:bg-sel data-[selected]:shadow-[inset_2px_0_0_var(--accent)]",
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
      <span className="justify-self-end text-sm whitespace-nowrap">{meta}</span>
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
    </button>
  );
}

export function InspectorPane({
  className,
  ...props
}: React.ComponentProps<"section">) {
  return (
    <section
      data-slot="inspector-pane"
      className={cn("flex min-h-0 min-w-0 flex-1 flex-col", className)}
      {...props}
    />
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
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "@container flex min-h-0 min-w-0 flex-1 flex-col gap-3.5 overflow-y-auto px-5 pt-4 pb-5 [&>*]:shrink-0",
        className,
      )}
      {...props}
    />
  );
}
