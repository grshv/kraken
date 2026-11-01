import {dateTime,localTime} from './reference/model';
import type {Snapshot} from './reference/model';
import { fixtures } from './fixtures';
export type Domain={d:string;s:number|boolean;h:number;isnew?:number;del?:boolean};
export type Client={id:string;name:string;desc:string;kind:string;nets:string[];pol:string;tr:string;login:string;req:number;den:number;bytes:string;last:string;tun:number;on:boolean;changed?:string;isnew?:boolean;localDomains?:Domain[];policyIDs?:string[];access?:'list'|'all_web'|'none'};
export type Policy={id:string;name:string;mode:string;dom:Domain[];own?:boolean};
export type Notice={d:string;n:number;why:string};
export type Data={snapshot?:Snapshot;clients:Client[];policies:Policy[];blocked:Domain[];unknown:{ip:string;den:number;dst:string[];last:string}[];denied:Record<string,Notice[]>};
export type Event={id:string;at:string;client:string;ip:string;host:string;port:string;method:string;status:number;denied:boolean;bytes:number;count:number};
export type Change={id:string;title:string;detail:string;client?:string;effect:'Расширяет доступ'|'Ограничивает доступ'|'Меняет настройки'|'Меняет подключение'};
export type View='events'|'domains';
export type Range={from:string;to:string};
export type Filters={search:string;host?:string;clients:string[];result:'all'|'denied'|'allowed';methods:string[];reasons:string[];range:Range;period:string};
export const DEMO_END='2026-10-30T14:05';
export const num=(n:number)=>n.toLocaleString('ru-RU');
export const copy=<T,>(value:T):T=>structuredClone(value);
export const initialData=copy(fixtures.data) as unknown as Data;
initialData.clients.forEach(c=>{if(c.login==='dev-*')c.login='dev-anna'});
// Whole-web access belongs to a client, not to an empty shared policy.
initialData.clients.forEach(c=>{if(initialData.policies.find(p=>p.id===c.pol)?.mode==='all'){c.access='all_web';c.pol='';c.policyIDs=[]}});
initialData.policies=initialData.policies.filter(p=>p.mode!=='all');
export const events:Event[]=fixtures.events.map((row,i)=>{const end=row[4].lastIndexOf(':');return {id:'event-'+i,at:'2026-10-30T'+row[0],client:row[1],ip:row[2],method:row[3],host:row[4].slice(0,end),port:row[4].slice(end+1),status:+row[5],denied:row[6]==='d',bytes:row[6]==='d'?0:[14200,803200,52700,9216][i%4],count:1}}).sort((a,b)=>b.at.localeCompare(a.at));
export const fmtBytes=(n:number)=>n>=1048576?(n/1048576).toLocaleString('ru-RU',{maximumFractionDigits:1})+' МБ':n>=1024?(n/1024).toLocaleString('ru-RU',{maximumFractionDigits:1})+' КБ':n+' Б';
export const policyOf=(d:Data,c:Client)=>d.policies.find(p=>p.id===c.pol);
export const covers=(d:Data,c:Client,host:string)=>{const ps=d.policies.filter(p=>(c.policyIDs??[c.pol]).includes(p.id));return !!c.on&&c.access!=='none'&&(c.access==='all_web'||ps.some(p=>p.mode==='all')||[...ps.flatMap(p=>p.dom),...(c.localDomains||[])].some(x=>!x.del&&(x.d===host||(x.s&&host.endsWith('.'+x.d)))))};
export function preset(period:string):Range{const days={'1 ч':1/24,'24 ч':1,'7 дн':7,'30 дн':30}[period]??1;return {from:new Date(Date.parse(DEMO_END+'Z')-days*86400000).toISOString().slice(0,16),to:DEMO_END}}
export const defaultFilters=():Filters=>({search:'',clients:[],result:'all',methods:[],reasons:[],range:preset('24 ч'),period:'24 ч'});
export const reason=(e:Event)=>!e.denied?'':e.client==='?'?'unknown':e.port==='587'?'port':'policy';
export const reasons:Record<string,string>={unknown:'Источник не описан',port:'Порт закрыт',policy:'Нет в списке клиента'};
export function filterEvents(all:Event[],f:Filters,except=''):Event[]{return all.filter(e=>e.at>=f.range.from&&e.at<=f.range.to+':59'&&(!f.host||e.host===f.host)&&(except==='clients'||!f.clients.length||f.clients.includes(e.client))&&(except==='result'||f.result==='all'||(f.result==='denied')===e.denied)&&(except==='methods'||!f.methods.length||f.methods.includes(e.method))&&(except==='reasons'||!f.reasons.length||f.reasons.includes(reason(e)))&&(!f.search||[e.host,e.ip,e.method].join(' ').toLowerCase().includes(f.search.toLowerCase())))}
export const eventCount=(rows:Event[])=>rows.reduce((n,e)=>n+e.count,0);
export function formatEventTime(at:string,range:Range):string{return range.from.slice(0,10)===range.to.slice(0,10)?localTime(at):dateTime(at,true,true)}
export function formatRange(range:Range):string{return dateTime(range.from,false)+' — '+dateTime(range.to,false)+' МСК'}
// Диапазон задан московским настенным временем; часовой пояс браузера не участвует.
export function compactRange(range:Range):string {
 const months=['янв.','февр.','мар.','апр.','мая','июн.','июл.','авг.','сент.','окт.','нояб.','дек.'];
 const from=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}:\d{2})/.exec(range.from),to=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}:\d{2})/.exec(range.to);
 if(!from||!to)return formatRange(range);
 const years=from[1]!==to[1];
 const day=(parts:RegExpExecArray)=>Number(parts[3])+' '+months[Number(parts[2])-1]+(years?' '+parts[1]:'');
 return range.from.slice(0,10)===range.to.slice(0,10) ? day(from)+', '+from[4]+'–'+to[4] : day(from)+', '+from[4]+' — '+day(to)+', '+to[4];
}
export type Group={host:string;events:Event[];count:number;denied:number;bytes:number;last:string;sources:number;ports:string[]};
export function groupEvents(all:Event[]):Group[]{const groups=new Map<string,Group>();all.forEach(e=>{const g=groups.get(e.host)||{host:e.host,events:[],count:0,denied:0,bytes:0,last:'',sources:0,ports:[]};g.events.push(e);g.count+=e.count;g.denied+=e.denied?e.count:0;g.bytes+=e.bytes;g.last=e.at>g.last?e.at:g.last;g.sources=new Set(g.events.map(x=>x.ip)).size;g.ports=[...new Set(g.events.map(x=>x.port))];groups.set(e.host,g)});return [...groups.values()].sort((a,b)=>b.count-a.count||b.last.localeCompare(a.last)||a.host.localeCompare(b.host))}
