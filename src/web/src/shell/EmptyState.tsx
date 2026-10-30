import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";

// Empty-state card: icon, short title, helper text, optional action.
export function EmptyState({
  icon,
  title,
  children,
  actionLabel,
  onAction,
}: {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  actionLabel?: string;
  onAction?: () => void;
}) {
  return (
    <div className="flex max-w-[560px] flex-col items-start gap-2 rounded-lg border border-dashed p-6 text-muted-foreground">
      {icon && (
        <div className="text-muted-foreground [&_svg]:size-[22px]">{icon}</div>
      )}
      <div className="text-lg font-semibold text-foreground">{title}</div>
      {children && <div>{children}</div>}
      {actionLabel && onAction && (
        <Button size="sm" variant="outline" onClick={onAction}>
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
