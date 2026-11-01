import type { ReactNode } from 'react';
export {Button} from '@/components/ui/button';
export {Input} from '@/components/ui/input';
export {Textarea} from '@/components/ui/textarea';
export {Label} from '@/components/ui/label';
export {Badge} from '@/components/ui/badge';
export {Checkbox} from '@/components/ui/checkbox';
export {Switch} from '@/components/ui/switch';
export {RadioGroup,RadioGroupItem} from '@/components/ui/radio-group';
export {Tabs,TabsList,TabsTrigger,TabsContent} from '@/components/ui/tabs';
export {Separator} from '@/components/ui/separator';
export {ScrollArea} from '@/components/ui/scroll-area';
export {Collapsible,CollapsibleTrigger,CollapsibleContent} from '@/components/ui/collapsible';
export {Popover,PopoverTrigger,PopoverContent} from '@/components/ui/popover';
export {Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter} from '@/components/ui/dialog';
export {AlertDialog,AlertDialogContent,AlertDialogHeader,AlertDialogTitle,AlertDialogDescription,AlertDialogFooter,AlertDialogCancel,AlertDialogAction} from '@/components/ui/alert-dialog';
export {Sheet,SheetContent,SheetHeader,SheetTitle,SheetDescription,SheetFooter} from '@/components/ui/sheet';
export {DropdownMenu,DropdownMenuTrigger,DropdownMenuContent,DropdownMenuItem,DropdownMenuSeparator,DropdownMenuLabel} from '@/components/ui/dropdown-menu';
export {Command,CommandInput,CommandList,CommandEmpty,CommandGroup,CommandItem} from '@/components/ui/command';
export {Table,TableHeader,TableBody,TableRow,TableHead,TableCell} from '@/components/ui/table';
export {ListPane,ListPaneHeader,ListPaneBody,ListGroupLabel,ListRow,InspectorPane,InspectorHeader,InspectorBody,DetailPane} from '@/components/kraken/layout';
export {Callout,Field,SettingRow,KeyValue,Kpi,KpiGroup,Chip} from '@/components/kraken/blocks';
import {Card,CardHeader,CardTitle,CardDescription,CardAction,CardContent} from '@/components/ui/card';
import {Select,SelectTrigger,SelectValue,SelectContent,SelectItem} from '@/components/ui/select';
import {ToggleGroup,ToggleGroupItem} from '@/components/ui/toggle-group';
export {Card,CardHeader,CardTitle,CardDescription,CardContent};
export function Panel({title,description,action,children,className=''}:{title:string;description?:ReactNode;action?:ReactNode;children:ReactNode;className?:string}){return <Card className={className}><CardHeader><div><CardTitle>{title}</CardTitle>{description&&<CardDescription>{description}</CardDescription>}</div>{action&&<CardAction>{action}</CardAction>}</CardHeader><CardContent>{children}</CardContent></Card>}
export function Options({value,onChange,items,label}:{value:string;onChange:(v:string)=>void;items:{value:string;label:string;disabled?:boolean}[];label:string}){return <Select value={value} onValueChange={onChange}><SelectTrigger aria-label={label} className="w-full"><SelectValue placeholder={label}/></SelectTrigger><SelectContent>{items.map(item=><SelectItem key={item.value} value={item.value} disabled={item.disabled}>{item.label}</SelectItem>)}</SelectContent></Select>}
export function Segments({value,onChange,items,label,className='',itemClassName=''}:{value:string;onChange:(v:string)=>void;items:[string,string][];label:string;className?:string;itemClassName?:string}){return <ToggleGroup type="single" variant="seg" size="sm" value={value} onValueChange={v=>v&&onChange(v)} aria-label={label} spacing={1} className={'overflow-hidden rounded-md border bg-surface-control p-0.5 '+className}>{items.map(([id,name])=><ToggleGroupItem key={id} value={id} className={itemClassName}>{name}</ToggleGroupItem>)}</ToggleGroup>}
