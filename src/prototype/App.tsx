import {newEventCount} from './shell/notification-count';
import {CommandPalette} from './CommandPalette';
import {Toaster} from "./components/ui/sonner";
import {toast} from "sonner";
import {Review} from './review';
import {PasswordDialog} from './reference/shell/PasswordDialog';
import {useEffect,useMemo,useState,type CSSProperties} from 'react';
import {toSnapshot} from './reference-adapter';
import {draftChanges} from './review-data';
import {scopedReview} from './review-scope';
import {Search,FlaskConical} from 'lucide-react';
import {useDemo} from './store';
import {useTheme,themeVariants} from './ThemeProvider';
import {KrakenMark} from './shell/KrakenMark';
import {ResizablePanelGroup} from './components/ui/resizable';
import {useDefaultLayout} from 'react-resizable-panels';
import {Tooltip,TooltipTrigger,TooltipContent} from './components/ui/tooltip';
import {SidebarProvider,Sidebar,SidebarContent,SidebarFooter,SidebarMenu,SidebarMenuItem,SidebarMenuButton,SidebarInset} from './components/ui/sidebar';
import {ReferenceScreens} from './reference-adapter';
import {navItems} from './shell/nav';
import {navIcons} from './shell/navIcons';
import {ProfileMenu} from './shell/ProfileMenu';
import {StatusIndicator} from './shell/StatusIndicator';
import {Requests} from './requests';
import {Button,Badge,Options,Switch,Label,Popover,PopoverTrigger,PopoverContent,Command,CommandInput,CommandList,CommandEmpty,CommandGroup,CommandItem,Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription,DialogFooter,Input,Sheet,SheetContent,SheetHeader,SheetTitle,SheetDescription,SheetFooter,AlertDialog,AlertDialogContent,AlertDialogHeader,AlertDialogTitle,AlertDialogDescription,AlertDialogFooter,AlertDialogCancel,AlertDialogAction,Callout} from './ui';

export function App() {
  const s=useDemo(), theme=useTheme(), layout=useDefaultLayout({id:'kraken-workspace-panels'});
  const [command,setCommand]=useState(false), [password,setPassword]=useState(false), [discard,setDiscard]=useState(false);
  useEffect(()=>{const handler=(e:KeyboardEvent)=>{if((e.metaKey||e.ctrlKey)&&e.key==='k'){e.preventDefault();setCommand(v=>!v)}};window.addEventListener('keydown',handler);return()=>window.removeEventListener('keydown',handler)},[]);
  useEffect(()=>{if(s.notice)toast(s.notice,{id:'kraken-notice'})},[s.notice]);
  const sections=navItems(s.manager?'manager':'admin').map(n=>[n.id,n.label,navIcons[n.id]] as const);
  const current=sections.find(([id])=>id===s.nav)?.[1] ?? 'Клиенты';
  const reviewChanges=useMemo(()=>{
    const active=toSnapshot(s.active),draft=toSnapshot(s.data);
    return s.manager?scopedReview(active,draft,s.revision,s.inScope).preview.changes??[]:draftChanges(active,draft);
  },[s.active,s.data,s.manager,s.revision]);
  const newEvents=newEventCount(s.data,s.marks,s.inScope,!s.manager);
  const railItem=([id,title,Icon]:typeof sections[number])=><SidebarMenuItem key={id} className="relative"><Tooltip><TooltipTrigger asChild>
    <SidebarMenuButton isActive={s.nav===id} className={'relative size-10 justify-center rounded-lg border-0 p-0 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground [&_svg]:size-[19px] '+(s.nav===id?'bg-sidebar-accent text-sidebar-accent-foreground':'bg-transparent text-sidebar-foreground')} aria-label={title} aria-description={id==='clients'&&newEvents>0?'Новых событий: '+newEvents:undefined} aria-current={s.nav===id?'page':undefined} data-nav={id} onClick={()=>s.setNav(id)}><Icon/>{id==='clients'&&newEvents>0&&<span data-slot="new-events-indicator" className="absolute top-1 right-1 size-2 rounded-full bg-warn" aria-hidden/>}</SidebarMenuButton>
  </TooltipTrigger><TooltipContent side="right">{title}</TooltipContent></Tooltip></SidebarMenuItem>;
  return <div className="h-dvh min-w-[1024px] overflow-hidden bg-background text-foreground">
    <SidebarProvider style={{'--sidebar-width':'64px'} as CSSProperties} className="h-full min-h-0">
      <Sidebar collapsible="none" role="navigation" aria-label="Разделы" className="items-center border-r border-sidebar-border">
        <div className="grid h-16 w-full shrink-0 place-items-center" title="Kraken"><KrakenMark className="size-7 text-foreground"/></div>
        <SidebarContent className="w-full items-center pt-3"><SidebarMenu className="w-10 gap-2">{sections.filter(([id])=>id!=='help'&&id!=='settings').map(railItem)}</SidebarMenu></SidebarContent>
        <SidebarFooter className="items-center gap-3 px-0 pb-4"><SidebarMenu className="w-10 gap-2">{sections.filter(([id])=>id==='help'||id==='settings').map(railItem)}</SidebarMenu><ProfileMenu onPassword={()=>setPassword(true)}/></SidebarFooter>
      </Sidebar>
      <SidebarInset id="main" className="min-h-0 min-w-0">
        <header className="flex h-16 shrink-0 items-center gap-3 border-b border-header-border bg-header px-6 text-header-foreground">
          <nav aria-label="Текущий раздел" className="flex min-w-0 items-center gap-3 text-base"><span className="text-header-muted">Kraken</span><span className="text-header-muted/60" aria-hidden>/</span><span className="truncate">{current}</span></nav>
          <span className="grow"/>
          <Button variant="ghost" size="sm" className="gap-2 text-header-muted" onClick={()=>setCommand(true)} aria-label="Клиент, IP, домен или действие… ⌘K"><Search/>Поиск <kbd className="ml-2 rounded border px-1 text-xs font-normal">⌘K</kbd></Button>
          <span className="mx-1 h-4 border-l"/>
          <StatusIndicator/>
          <Popover><PopoverTrigger asChild><Button variant="ghost" size="icon-sm" aria-label="Мок"><FlaskConical/></Button></PopoverTrigger><PopoverContent align="end" className="space-y-4"><strong>Сценарии предпросмотра</strong><Options label="Тема" value={theme.variant} onChange={theme.setVariant} items={themeVariants}/><Options label="Роль" value={s.role} onChange={v=>{s.setRole(v);s.openClient('ci01')}} items={[{value:'admin',label:'Администратор'},{value:'manager',label:'Менеджер'}]}/><Options label="Состояние Squid" value={s.env} onChange={s.setEnv} items={[{value:'ok',label:'Squid работает'},{value:'down',label:'Squid недоступен'}]}/><Label className="flex justify-between">Ошибка применения<Switch checked={s.applyFail} onCheckedChange={s.setApplyFail}/></Label><Label className="flex justify-between">Конфликт ревизий<Switch checked={s.conflict} onCheckedChange={s.setConflict}/></Label></PopoverContent></Popover>
        </header>
        {s.env!=='ok'&&<Callout tone="bad" className="rounded-none border-x-0 border-t-0 px-6 py-3 text-sm">Squid не отвечает с 14:03. Трафик клиентов сейчас не проходит. Kraken перезапускает Squid с последней рабочей ревизией r-{s.revision}.</Callout>}
        <ResizablePanelGroup orientation="horizontal" id="kraken-main" defaultLayout={layout.defaultLayout} onLayoutChanged={layout.onLayoutChanged} className="min-h-0 flex-1">{s.nav==='log'?<Requests/>:<ReferenceScreens/>}</ResizablePanelGroup>
        {s.changes.length>0&&<div className="flex h-14 shrink-0 items-center gap-3 border-t bg-header px-6"><span className="inline-flex items-center gap-2 rounded-md border border-warn-border bg-warn-bg px-2.5 py-1 text-warn"><span className="size-1.5 rounded-full bg-current"/><span className="font-medium">Черновик конфигурации</span></span><span className="text-sm text-muted-foreground">{reviewChanges.length} изм. · Не применён</span><span className="grow"/><Button size="sm" variant="ghost" onClick={()=>setDiscard(true)}>Сбросить</Button><Button size="sm" onClick={()=>s.setReview(true)}>Проверить и применить</Button></div>}
      </SidebarInset>
    </SidebarProvider>
    <CommandPalette open={command} onOpenChange={setCommand}/>
    <Toaster offset={{bottom:72}}/><Review/>
    <AlertDialog open={discard} onOpenChange={setDiscard}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>Сбросить черновик?</AlertDialogTitle><AlertDialogDescription>Все правки черновика будут потеряны. Действующий доступ сохранится.</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>Отмена</AlertDialogCancel><AlertDialogAction onClick={s.discard}>Сбросить</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
    <PasswordDialog open={password} onOpenChange={setPassword} onSubmit={async()=>{const name=s.manager?'anna':'admin';s.setAccounts(old=>({...old,[name]:{...old[name],sess:old[name].sess?.filter(row=>row[4])}}));s.setNotice('Пароль изменён, другие сессии завершены')}}/>
  </div>;
}
