import * as React from "react";
import { cn } from "cn";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";
import { Switch } from "../ui/switch";
import { Checkbox } from "../ui/checkbox";
import { SelectTrigger } from "../ui/select";
import { RadioGroup } from "../ui/radio-group";

// Wire the shared shadcn field composition to its controls, including fragments.
function describeControls(
  node: React.ReactNode,
  labelId: string,
  descriptionId: string | undefined,
  baseId: string,
) {
  let firstId: string | undefined,
    index = 0;
  const visit = (children: React.ReactNode): React.ReactNode =>
    React.Children.map(children, (child) => {
      if (!React.isValidElement(child)) return child;
      const el = child as React.ReactElement<Record<string, unknown>>;
      if (el.type === React.Fragment)
        return React.cloneElement(
          el,
          {},
          visit(el.props.children as React.ReactNode),
        );
      if (
        ![
          Input,
          Textarea,
          Switch,
          Checkbox,
          SelectTrigger,
          RadioGroup,
        ].includes(el.type as typeof Input)
      )
        return child;
      const ordinal = index++;
      const id =
        typeof el.props.id === "string"
          ? el.props.id
          : ordinal === 0
            ? baseId
            : baseId + "-" + ordinal;
      firstId ??= id;
      return React.cloneElement(el, {
        id,
        "aria-labelledby":
          el.props["aria-labelledby"] ||
          (el.props["aria-label"] ? undefined : labelId),
        "aria-describedby":
          [el.props["aria-describedby"], descriptionId]
            .filter(Boolean)
            .join(" ") || undefined,
      });
    });
  const children = visit(node);
  return { children, firstId };
}
import { Alert, AlertDescription } from "../ui/alert";
import { CardContent } from "../ui/card";
import {
  Field as FormField,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "../ui/field";
// Product wrappers compose shadcn primitives; they contain no control logic.
export function Callout({
  tone = "info",
  icon,
  actions,
  className,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  tone?: "info" | "warn" | "bad";
  icon?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <Alert
      role={tone === "bad" ? "alert" : undefined}
      className={cn(
        "px-3 py-2.5",
        tone === "bad"
          ? "border-bad/25 bg-bad-bg"
          : tone === "warn"
            ? "border-warn/25 bg-warn-bg"
            : "border-info/20 bg-info-bg",
        className,
      )}
      {...props}
    >
      <AlertDescription className="flex items-start gap-2.5 text-base">
        {icon && (
          <span
            className={cn(
              "mt-0.5 shrink-0 [&>svg]:size-4",
              tone === "bad" ? "text-bad" : tone === "warn" ? "text-warn" : "",
            )}
          >
            {icon}
          </span>
        )}
        <div className="min-w-0 grow">{children}</div>
        {actions && <div className="flex shrink-0 gap-2">{actions}</div>}
      </AlertDescription>
    </Alert>
  );
}
export function Chip({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="chip"
      className={cn(
        "inline-flex items-center gap-1 text-base whitespace-nowrap tabular-nums",
        className,
      )}
      {...props}
    />
  );
}
export function KpiGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="kpi-group"
      className={cn(
        "grid grid-cols-2 gap-0 overflow-hidden border-b pb-4 @[600px]:grid-cols-4 [&>*]:border-l [&>*:nth-child(odd)]:border-l-0 @[600px]:[&>*:nth-child(3)]:border-l",
        className,
      )}
      {...props}
    />
  );
}
// onClick turns the figure into a link to the requests it counts.
export function Kpi({
  label,
  value,
  hint,
  tone,
  onClick,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  hint?: React.ReactNode;
  tone?: "bad";
  onClick?: () => void;
}) {
  const body = (
    <>
      <span className="block min-h-[2.5em] w-full text-sm leading-tight font-normal text-muted-foreground">
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
    </>
  );
  return onClick ? (
    <CardContent className="flex min-w-0 p-0">
      <Button
        variant="ghost"
        onClick={onClick}
        className="h-auto w-full min-w-0 flex-col items-start gap-px rounded-none px-4 py-2 text-left font-normal"
      >
        {body}
      </Button>
    </CardContent>
  ) : (
    <CardContent className="flex min-w-0 flex-col gap-px px-4 py-2">
      {body}
    </CardContent>
  );
}
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
      {items.map(([key, value], i) => (
        <React.Fragment key={i}>
          <dt className="text-muted-foreground">{key}</dt>
          <dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{value}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}
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
  const uid = React.useId(),
    labelId = uid + "-label",
    descriptionId = help || error ? uid + "-description" : undefined;
  const controls = describeControls(
    children,
    labelId,
    descriptionId,
    htmlFor || uid,
  );
  return (
    <FormField
      data-invalid={!!error || undefined}
      className={cn("min-w-0 gap-1.5", className)}
    >
      <FieldLabel
        id={labelId}
        htmlFor={htmlFor || controls.firstId}
        className="text-base font-medium"
      >
        {label}
      </FieldLabel>
      {controls.children}
      {error ? (
        <FieldError id={descriptionId}>{error}</FieldError>
      ) : (
        help && <FieldDescription id={descriptionId}>{help}</FieldDescription>
      )}
    </FormField>
  );
}
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
  const uid = React.useId(),
    labelId = uid + "-label",
    descriptionId = help ? uid + "-description" : undefined;
  const controls = describeControls(control, labelId, descriptionId, uid);
  return (
    <FormField
      orientation="horizontal"
      className="items-center has-[>[data-slot=field-content]]:items-center gap-4 border-b py-3 last:border-b-0"
    >
      <FieldContent className="min-w-0">
        <FieldLabel
          id={labelId}
          htmlFor={controls.firstId}
          className="flex items-center gap-2 text-base font-medium"
        >
          {title}
          {badge}
        </FieldLabel>
        {help && <FieldDescription id={descriptionId}>{help}</FieldDescription>}
      </FieldContent>
      <div className="flex min-h-8 shrink-0 items-center justify-end">
        {controls.children}
      </div>
    </FormField>
  );
}
