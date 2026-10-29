import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "cn";

// Small building blocks from the prototype, built on Tailwind tokens from
// theme.css. Sections use these together with shadcn components (Card, Badge,
// Table, Tabs, Switch, Select, RadioGroup) instead of their own CSS classes.

const calloutVariants = cva(
  "flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-base [&>svg]:mt-0.5 [&>svg]:size-4 [&>svg]:shrink-0",
  {
    variants: {
      tone: {
        info: "border-info/20 bg-info-bg",
        warn: "border-warn/25 bg-warn-bg [&>svg]:text-warn",
        bad: "border-bad/25 bg-bad-bg [&>svg]:text-bad",
      },
    },
    defaultVariants: { tone: "info" },
  },
);

// Tinted notice: optional icon, then content; `actions` sit on the right.
export function Callout({
  tone,
  icon,
  actions,
  className,
  children,
  ...props
}: React.ComponentProps<"div"> &
  VariantProps<typeof calloutVariants> & {
    icon?: React.ReactNode;
    actions?: React.ReactNode;
  }) {
  return (
    <div
      role={tone === "bad" ? "alert" : undefined}
      className={cn(calloutVariants({ tone }), className)}
      {...props}
    >
      {icon}
      <div className="min-w-0 grow">{children}</div>
      {actions && <div className="flex shrink-0 gap-2">{actions}</div>}
    </div>
  );
}

// Machine value in a muted pill: domain, IP, CIDR.
export function Chip({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      className={cn(
        "inline-flex h-[22px] items-center gap-1 rounded-md bg-muted px-1.5 font-mono text-sm whitespace-nowrap",
        className,
      )}
      {...props}
    />
  );
}

// Row of KPI cells sharing one border (4 across, 2 when narrow).
export function KpiGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "grid grid-cols-2 overflow-hidden rounded-lg border bg-card @[600px]:grid-cols-4 [&>*]:border-l [&>*:first-child]:border-l-0",
        className,
      )}
      {...props}
    />
  );
}

export function Kpi({
  label,
  value,
  hint,
  tone,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: "bad";
}) {
  return (
    <div className="flex min-w-0 flex-col gap-px px-3.5 py-2.5">
      <span className="truncate text-sm font-medium text-muted-foreground">
        {label}
      </span>
      <span
        className={cn(
          "truncate text-xl font-semibold tracking-tight tabular-nums",
          tone === "bad" && "text-bad",
        )}
      >
        {value}
      </span>
      {hint && (
        <span className="truncate text-sm text-muted-foreground">{hint}</span>
      )}
    </div>
  );
}

// Definition list: muted terms on the left, values on the right.
export function KeyValue({
  items,
  className,
}: {
  items: [React.ReactNode, React.ReactNode][];
  className?: string;
}) {
  return (
    <dl
      className={cn(
        "m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5 text-base",
        className,
      )}
    >
      {items.map(([k, v], i) => (
        <React.Fragment key={i}>
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{v}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

// Form field: label above the control, help or error below.
export function Field({
  label,
  htmlFor,
  help,
  error,
  className,
  children,
}: {
  label: React.ReactNode;
  htmlFor?: string;
  help?: React.ReactNode;
  error?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-base font-medium">
        {label}
      </label>
      {children}
      {error ? (
        <div className="text-sm text-bad">{error}</div>
      ) : (
        help && <div className="text-sm text-muted-foreground">{help}</div>
      )}
    </div>
  );
}

// Settings row: title and help on the left, control (Switch, Input) on the right.
export function SettingRow({
  title,
  help,
  badge,
  control,
}: {
  title: React.ReactNode;
  help?: React.ReactNode;
  badge?: React.ReactNode;
  control: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3 border-b py-2.5 last:border-b-0">
      <div className="min-w-0 grow">
        <div className="flex items-center gap-2 font-medium">
          {title}
          {badge}
        </div>
        {help && <div className="text-sm text-muted-foreground">{help}</div>}
      </div>
      <div className="shrink-0">{control}</div>
    </div>
  );
}
