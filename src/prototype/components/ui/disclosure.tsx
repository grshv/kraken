import type {ReactNode} from 'react';
import {ChevronRight} from 'lucide-react';
import {cn} from 'cn';
import {Button} from './button';
import {Collapsible, CollapsibleContent, CollapsibleTrigger} from './collapsible';

export function Disclosure({label,children,className}:{label:string;children:ReactNode;className?:string}) {
  return <Collapsible className={cn('text-sm text-muted-foreground',className)}>
    <CollapsibleTrigger asChild><Button size="xs" variant="ghost" className="group min-h-8 h-auto w-full justify-start gap-2 px-2 py-1.5 text-left text-inherit whitespace-normal"><ChevronRight aria-hidden className="transition-transform group-data-[state=open]:rotate-90"/><span className="min-w-0 flex-1">{label}</span></Button></CollapsibleTrigger>
    <CollapsibleContent className="pt-2 pb-1 pl-[30px] [&>:first-child]:mt-0">{children}</CollapsibleContent>
  </Collapsible>;
}
