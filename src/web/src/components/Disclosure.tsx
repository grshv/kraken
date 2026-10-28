import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "./ui/collapsible";
import { Button } from "./ui/button";
export function Disclosure({
  title,
  children,
  className = "",
}: {
  title: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Collapsible data-slot="collapsible" className={`disclosure ${className}`}>
      <CollapsibleTrigger asChild>
        <Button variant="ghost" className="disclosure-trigger justify-start">
          <ChevronDown className="size-4 shrink-0" />
          {title}
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="disclosure-content">
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
}
