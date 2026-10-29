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
    <div className="empty-state">
      {icon && <div className="empty-state-icon">{icon}</div>}
      <div className="empty-state-title">{title}</div>
      {children && <div className="empty-state-text">{children}</div>}
      {actionLabel && onAction && (
        <Button size="sm" variant="outline" onClick={onAction}>
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
