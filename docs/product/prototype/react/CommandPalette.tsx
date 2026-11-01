import {useEffect,useState} from 'react';
import {Plus,KeyRound,Ban,Play,Globe,Shield,Users} from 'lucide-react';
import {useDemo} from './store';
import {defaultFilters,events} from './model';
import {navItems} from './shell/nav';
import {navIcons} from './shell/navIcons';
import {Command,CommandInput,CommandList,CommandEmpty,CommandGroup,CommandItem,Dialog,DialogContent,DialogHeader,DialogTitle,DialogDescription} from './ui';

export function CommandPalette({open,onOpenChange}:{open:boolean;onOpenChange:(open:boolean)=>void}) {
  const s=useDemo(),[query,setQuery]=useState('');
  useEffect(()=>{if(open)setQuery('')},[open]);
  const run=(action:()=>void)=>{onOpenChange(false);action()};
  const clients=s.data.clients.filter(c=>s.inScope(c.id));
  const policies=s.data.policies.filter(p=>!s.manager||clients.some(c=>(c.policyIDs??[c.pol]).includes(p.id)));
  const hosts=[...new Set([
    ...events.filter(e=>s.inScope(e.client)).map(e=>e.host),
    ...policies.flatMap(p=>p.dom.map(d=>d.d)),
    ...clients.flatMap(c=>c.localDomains?.map(d=>d.d)||[]),
    ...(!s.manager?s.data.blocked.map(d=>d.d):[]),
  ])].sort();
  const actions=[
    {id:'new-client',label:'Новый клиент',Icon:Plus,run:()=>s.openSection('clients','new-client')},
    {id:'new-proxy-login',label:'Новый логин прокси',Icon:KeyRound,run:()=>s.openSection('accounts','new-proxy-login')},
    ...(!s.manager?[{id:'blocked-domains',label:'Запретить домен для всех',Icon:Ban,run:()=>s.openSection('policies','blocked-domains')}]:[]),
    {id:'review',label:'Проверить и применить черновик',Icon:Play,run:()=>s.setReview(true)},
  ];
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="w-[calc(100%-3rem)] max-h-[calc(100dvh-3rem)] gap-0 overflow-hidden p-0 sm:max-w-[760px] [&>[data-slot=dialog-close]]:inline-flex [&>[data-slot=dialog-close]]:size-8 [&>[data-slot=dialog-close]]:items-center [&>[data-slot=dialog-close]]:justify-center [&>[data-slot=dialog-close]]:rounded-md"><DialogHeader className="sr-only"><DialogTitle>Поиск и действия</DialogTitle><DialogDescription>Поиск клиентов, адресов, доменов, политик и разделов</DialogDescription></DialogHeader><Command className="[&_[data-slot=command-input-wrapper]]:h-16 [&_[data-slot=command-input-wrapper]]:gap-3 [&_[data-slot=command-input-wrapper]]:pl-5 [&_[data-slot=command-input-wrapper]]:pr-16 [&_[cmdk-group]]:p-2 [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pt-3 [&_[cmdk-group-heading]]:pb-2 [&_[cmdk-item]]:min-h-10 [&_[cmdk-item]]:gap-3 [&_[cmdk-item]]:rounded-md [&_[cmdk-item]]:px-3 [&_[cmdk-item]]:py-2 [&_[cmdk-item]]:text-base [&_[cmdk-item][data-selected=true]]:bg-sel [&_[cmdk-item][data-selected=true]]:text-sel-foreground"><CommandInput className="h-16 min-w-0 text-base" placeholder="Клиент, IP, домен или действие…" value={query} onValueChange={setQuery}/><CommandList className="h-[min(520px,calc(100dvh-112px))] max-h-none scroll-py-2"><CommandEmpty>Ничего не найдено</CommandEmpty>
    <CommandGroup heading="Действия">{actions.map(a=><CommandItem key={a.id} value={a.label} onSelect={()=>run(a.run)}><a.Icon/>{a.label}</CommandItem>)}</CommandGroup>
    <CommandGroup heading="Клиенты">{clients.map(c=><CommandItem key={c.id} value={'Клиент '+c.name+' '+c.nets.join(' ')} onSelect={()=>run(()=>s.openClient(c.id))}><Users/><span className="min-w-0 flex-1 truncate" title={c.name}>{c.name}</span><span className="ml-auto max-w-[40%] truncate text-sm text-muted-foreground" title={c.nets.join(", ")}>{c.nets[0]}</span></CommandItem>)}</CommandGroup>
    {query.trim()&&<CommandGroup heading="Домены">{hosts.map(host=>{const who=[...new Set(events.filter(e=>e.host===host&&s.inScope(e.client)).map(e=>e.client==='?'?'не описан':s.data.clients.find(c=>c.id===e.client)?.name))].filter(Boolean);return <CommandItem key={host} value={'Домен '+host} onSelect={()=>run(()=>{s.setJournalIntent({id:Date.now(),view:'domains',filters:{...defaultFilters(),host}});s.setNav('log')})}><Globe/>{host}<span className="ml-auto truncate text-sm text-muted-foreground">{who.length?who.join(', '):'нет запросов'} · в журнале</span></CommandItem>})}</CommandGroup>}
    {query.trim()&&!s.manager&&<CommandGroup heading="Запретить для всех">{hosts.filter(host=>!s.data.blocked.some(b=>b.d===host)).map(host=><CommandItem key={host} value={'Запретить для всех '+host} onSelect={()=>run(()=>s.block(host))}>{host}<span className="ml-auto text-sm text-muted-foreground">в черновик</span></CommandItem>)}</CommandGroup>}
    <CommandGroup heading="Политики">{policies.map(p=><CommandItem key={p.id} value={'Политика '+p.name} onSelect={()=>run(()=>s.openSection('policies',`policy:${p.id}`))}><Shield/>{p.name}</CommandItem>)}</CommandGroup>
    <CommandGroup heading="Разделы">{navItems(s.manager?'manager':'admin').map(n=>{const Icon=navIcons[n.id];return <CommandItem key={n.id} value={'Раздел '+n.label} onSelect={()=>run(()=>s.setNav(n.id))}><Icon/>{n.label}</CommandItem>})}</CommandGroup>
  </CommandList></Command></DialogContent></Dialog>;
}
