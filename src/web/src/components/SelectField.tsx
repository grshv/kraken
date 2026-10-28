import type { ComponentProps, ReactNode } from "react";
import { Select, SelectContent, SelectTrigger, SelectValue } from "./ui/select";

type Props = Omit<
  ComponentProps<typeof SelectTrigger>,
  "value" | "onChange" | "children"
> & {
  value: string;
  onValueChange: (value: string) => void;
  children: ReactNode;
};
// Empty filters are a real choice; Radix reserves the empty string for placeholder.
export function SelectField({
  value,
  onValueChange,
  children,
  className,
  ...props
}: Props) {
  return (
    <div data-slot="select-field">
      <Select
        value={value || ":empty:"}
        onValueChange={(v) => onValueChange(v === ":empty:" ? "" : v)}
        disabled={props.disabled}
      >
        <SelectTrigger className={className} {...props}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>{children}</SelectContent>
      </Select>
    </div>
  );
}
