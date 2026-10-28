import type { ComponentProps } from "react";
import { Card } from "./ui/card";
export function Panel({
  className = "",
  ...props
}: ComponentProps<typeof Card>) {
  return (
    <Card
      role={props["aria-label"] ? "region" : undefined}
      className={`app-panel ${className}`}
      {...props}
    />
  );
}
