import {useState} from 'react';
import {ChevronDown,ChevronUp} from 'lucide-react';
import {Bar,BarChart,CartesianGrid,XAxis,YAxis} from 'recharts';
import {ChartContainer,ChartTooltip,ChartTooltipContent,type ChartConfig} from '@/components/ui/chart';
import {Button} from './ui';
import {useDemo} from './store';
import {clientSeries} from './chart-data';
import {localTime} from './reference/model';
import {num,fmtBytes,eventCount,groupEvents,type Event,type Filters} from './model';
import {cn} from 'cn';

const config={allowed:{label:'Разрешено',color:'var(--s-allow)'},denied:{label:'Отказано',color:'var(--s-deny)'}} satisfies ChartConfig;
const KEY='kraken-log-summary';
function remembered(){try{return localStorage.getItem(KEY)!=='collapsed'}catch{return true}}
function remember(open:boolean){try{localStorage.setItem(KEY,open?'open':'collapsed')}catch{/* storage may be unavailable */}}

// TrafficSummary: the whole picture for the journal's period and filters —
// totals, allowed/denied over time, top domains and top clients. Every figure
// narrows the journal below to exactly the requests it counts.
export function TrafficSummary({items,period,f,patch}:{items:Event[];period:string;f:Filters;patch:(p:Partial<Filters>)=>void}){
 const s=useDemo(),[open,setOpen]=useState(remembered);
 const denied=eventCount(items.filter(e=>e.denied)),total=eventCount(items),bytes=items.reduce((n,e)=>n+e.bytes,0);
 const sources=[...new Set(items.filter(e=>e.client==='?').map(e=>e.ip))];
 const domains=groupEvents(items).slice(0,5);
 const byClient=[...items.reduce((m,e)=>{const r=m.get(e.client)||{id:e.client,count:0,denied:0};r.count+=e.count;r.denied+=e.denied?e.count:0;m.set(e.client,r);return m},new Map<string,{id:string;count:number;denied:number}>()).values()].sort((a,b)=>b.count-a.count).slice(0,5);
 const name=(id:string)=>id==='?'?'Не описан':s.data.clients.find(c=>c.id===id)?.name||id;
 const series=clientSeries(items).map(b=>({...b,allowed:b.requests-b.denied}));
 const toggle=()=>{setOpen(v=>{remember(!v);return !v})};
 const kpi=(label:string,value:string,onClick:(()=>void)|null,active=false,tone?:'bad')=><Button variant="ghost" disabled={!onClick} onClick={onClick??undefined} aria-pressed={onClick?active:undefined} className={cn('h-auto min-w-0 flex-col items-start gap-0 px-2.5 py-1.5 text-left font-normal disabled:opacity-100',active&&'bg-sel')}><span className="text-sm text-muted-foreground">{label}</span><span className={cn('text-xl font-semibold tabular-nums',tone==='bad'&&value!=='0'&&'text-bad')}>{value}</span></Button>;
 const row=(key:string,label:React.ReactNode,count:number,deniedCount:number,onClick:()=>void,active:boolean,mono=false)=><li key={key}><Button variant="ghost" onClick={onClick} aria-pressed={active} className={cn('grid h-auto w-full grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3 px-2 py-1 text-left text-base font-normal',active&&'bg-sel')}><span className={cn('truncate',mono&&'font-mono text-sm')}>{label}</span><span className="text-sm tabular-nums"><span>{num(count)}</span>{deniedCount>0&&<span className="text-bad"> · {num(deniedCount)} отк.</span>}</span></Button></li>;
 return <section aria-label={'Сводка трафика за '+period} className="@container border-b px-2 py-2">
  <div className="flex flex-wrap items-end gap-1">
   {kpi('Запросы',num(total),f.result!=='all'?()=>patch({result:'all'}):null)}
   {kpi('Отказы',num(denied),()=>patch({result:f.result==='denied'?'all':'denied'}),f.result==='denied','bad')}
   {kpi('Объём',fmtBytes(bytes),null)}
   {kpi('Неописанные источники',num(sources.length),sources.length?()=>patch({clients:f.clients.includes('?')?f.clients.filter(x=>x!=='?'):[...f.clients,'?']}):null,f.clients.includes('?'))}
   <span className="grow"/>
   <span className="self-center pr-1 text-sm text-muted-foreground">за {period==='custom'?'выбранный период':period}</span>
   <Button size="xs" variant="ghost" className="self-center" aria-expanded={open} onClick={toggle}>{open?<ChevronUp/>:<ChevronDown/>}{open?'Свернуть':'Подробнее'}</Button>
  </div>
  {open&&<div className="grid grid-cols-1 gap-x-6 gap-y-3 px-2 pt-2 pb-1 @[600px]:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,1fr)]">
   <div className="min-w-0"><h3 className="mb-1 text-sm font-medium text-muted-foreground">Запросы по времени</h3><ChartContainer config={config} className="aspect-auto h-[92px] w-full"><BarChart data={series} margin={{top:2,right:0,bottom:0,left:0}}><CartesianGrid vertical={false} strokeDasharray="3 3"/><XAxis dataKey="start" tickFormatter={v=>localTime(String(v),false)} tick={{fill:'var(--muted-foreground)'}} tickLine={false} axisLine={false} minTickGap={40}/><YAxis width={28} tickCount={3} allowDecimals={false} tick={{fill:'var(--muted-foreground)'}} tickLine={false} axisLine={false}/><ChartTooltip content={<ChartTooltipContent labelFormatter={v=>localTime(String(v),false)}/>}/><Bar dataKey="allowed" stackId="r" fill="var(--color-allowed)" isAnimationActive={false}/><Bar dataKey="denied" stackId="r" fill="var(--color-denied)" isAnimationActive={false}/></BarChart></ChartContainer></div>
   <div className="min-w-0"><h3 className="mb-1 text-sm font-medium text-muted-foreground">Домены</h3><ul>{domains.map(g=>row(g.host,g.host,g.count,g.denied,()=>patch({host:f.host===g.host?undefined:g.host}),f.host===g.host,true))}</ul></div>
   <div className="min-w-0"><h3 className="mb-1 text-sm font-medium text-muted-foreground">Клиенты</h3><ul>{byClient.map(c=>row(c.id,name(c.id),c.count,c.denied,()=>patch({clients:f.clients.includes(c.id)?f.clients.filter(x=>x!==c.id):[c.id]}),f.clients.length===1&&f.clients[0]===c.id))}</ul></div>
  </div>}
 </section>;
}
