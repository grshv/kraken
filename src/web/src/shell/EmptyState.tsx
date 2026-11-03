import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
  EmptyDescription,
  EmptyContent,
} from "@/components/ui/empty";
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
    <Empty className="max-w-[560px] items-start border border-dashed p-6">
      <EmptyHeader className="items-start text-left">
        {icon && <EmptyMedia variant="icon">{icon}</EmptyMedia>}
        <EmptyTitle>{title}</EmptyTitle>
        {children && <EmptyDescription>{children}</EmptyDescription>}
      </EmptyHeader>
      {actionLabel && onAction && (
        <EmptyContent className="items-start">
          <Button size="sm" variant="outline" onClick={onAction}>
            {actionLabel}
          </Button>
        </EmptyContent>
      )}
    </Empty>
  );
}
