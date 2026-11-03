import * as React from "react";
import { cn } from "cn";

function Card({
  className,
  variant = "section",
  ...props
}: React.ComponentProps<"div"> & { variant?: "section" | "panel" }) {
  return (
    <div
      data-slot="card"
      data-variant={variant}
      className={cn(
        "group/card flex min-w-0 flex-col gap-4 text-card-foreground",
        variant === "panel"
          ? "rounded-lg border bg-card py-4"
          : "rounded-lg border bg-card p-4",
        className,
      )}
      {...props}
    />
  );
}

function CardHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-header"
      className={cn(
        "@container/card-header grid auto-rows-min grid-cols-[minmax(0,1fr)] items-center gap-x-3 gap-y-1.5 [&:has(>[data-slot=card-description])>[data-slot=card-action]]:row-span-2 group-data-[variant=panel]/card:px-4 has-data-[slot=card-action]:grid-cols-[minmax(0,1fr)_auto] [.border-b]:pb-3",
        className,
      )}
      {...props}
    />
  );
}

function CardTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-title"
      className={cn("text-lg leading-snug font-medium", className)}
      {...props}
    />
  );
}

function CardDescription({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-description"
      className={cn("col-start-1 text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

function CardAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-action"
      className={cn(
        "col-start-2 row-start-1 self-center justify-self-end",
        className,
      )}
      {...props}
    />
  );
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-content"
      className={cn("group-data-[variant=panel]/card:px-4", className)}
      {...props}
    />
  );
}

function CardFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-footer"
      className={cn(
        "flex items-center group-data-[variant=panel]/card:px-4 [.border-t]:pt-3",
        className,
      )}
      {...props}
    />
  );
}

export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardAction,
  CardDescription,
  CardContent,
};
