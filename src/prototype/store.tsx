import {createContext,useContext,useState,type ReactNode} from 'react';
import {fixtures} from './fixtures';
import {copy,initialData,policyOf,type Data,type Client,type Change,type Filters,type View} from './model';
export type Account={t:string;role?:string;on:boolean;last?:string;pwd:string;fail?:number;clients?:string[];nets?:string[];req?:string;bad?:string;ips?:[string,number][];sess?:[string,string,string,string,number][]};
export type Audit={title:string;who:string;t:string;day:string;type:string;sum:string;rev?:string;ch?:string[][];kv?:string[][]};
function useDemoStore(){
 const [data,setData]=useState<Data>(copy(initialData));const [active,setActive]=useState<Data>(()=>{const d=copy(initialData);d.clients.find(c=>c.id==='dev')!.tr='HTTP';d.clients.find(c=>c.id==='dev')!.login='';d.blocked=d.blocked.filter(x=>!x.isnew);return d});
 const [changes,setChanges]=useState<Change[]>([{id:'dev-tls',client:'dev',title:'dev-laptops: только TLS с логином',detail:'Новые соединения потребуют TLS и логин. Сейчас действует HTTP без логина.',effect:'Меняет подключение'},{id:'blocked-tracker',title:'Запрещено для всех: tracker.example',detail:'После применения новые запросы к домену получат отказ.',effect:'Ограничивает доступ'}]);
 const [nav,setNav]=useState('clients'),[selected,setSelected]=useState('ci01'),[tab,setTab]=useState('overview'),[policy,setPolicy]=useState('build'),[setting,setSetting]=useState('protect'),[account,setAccount]=useState('admin');
 const [role,setRole]=useState('admin'),[env,setEnv]=useState('ok'),[applyFail,setApplyFail]=useState(false),[conflict,setConflict]=useState(false),[revision,setRevision]=useState(41),[notice,setNotice]=useState(''),[review,setReview]=useState(false);
 const [marks,setMarks]=useState<Record<string,string>>({}),[accounts,setAccounts]=useState<Record<string,Account>>(copy(fixtures.accounts) as unknown as Record<string,Account>),[audit,setAudit]=useState<Audit[]>(copy(fixtures.audit) as unknown as Audit[]);
 const [requestIntent,setRequestIntent]=useState<{id:number;view:View}>({id:0,view:'events'}),[journalIntent,setJournalIntent]=useState<{id:number;view:View;filters:Filters}|null>(null);
 const [sectionIntent,setSectionIntent]=useState<{id:number;nav:string;intent:import('./reference/sections/sectionProps').SectionIntent}|null>(null);
 const openSection=(nav:string,intent:import('./reference/sections/sectionProps').SectionIntent)=>{setNav(nav);setSectionIntent({id:Date.now(),nav,intent})};
 const manager=role==='manager',inScope=(id:string)=>!manager||['build','ci01'].includes(id);
 const update=(fn:(next:Data)=>void)=>setData(d=>{const next=copy(d);fn(next);return next});
 const change=(v:Change)=>{setChanges(a=>[...a.filter(x=>x.id!==v.id),v]);setNotice('Добавлено в черновик; трафик пока не изменился')};
 const mark=(ids:string[],status:string)=>setMarks(v=>({...v,...Object.fromEntries(ids.map(id=>[id,status]))}));
 const allow=(cid:string,host:string)=>{const c=data.clients.find(c=>c.id===cid);if(!c||!inScope(cid))return;update(d=>{const x=d.clients.find(c=>c.id===cid)!;x.localDomains??=[];if(!x.localDomains.some(v=>v.d===host))x.localDomains.push({d:host,s:0,h:0,isnew:1})});change({id:cid+':'+host,client:cid,title:c.name+': '+host,detail:'Разрешить только этому клиенту. Общая политика не меняется.',effect:'Расширяет доступ'})};
 // allowInPolicy adds a domain to a shared policy: every client attached to it gains access.
 const allowInPolicy=(pid:string,host:string)=>{const p=data.policies.find(p=>p.id===pid);if(!p)return;const users=data.clients.filter(c=>(c.policyIDs??[c.pol]).includes(pid));if(manager&&users.some(c=>!inScope(c.id)))return;update(d=>{const x=d.policies.find(p=>p.id===pid)!;if(!x.dom.some(v=>v.d===host))x.dom.push({d:host,s:0,h:0,isnew:1})});change({id:'policy:'+pid+':'+host,title:'Политика «'+p.name+'»: '+host,detail:'Затронет: '+users.map(c=>c.name).join(', '),effect:'Расширяет доступ'})};
 const block=(host:string)=>{if(manager)return;update(d=>{if(!d.blocked.some(x=>x.d===host))d.blocked.push({d:host,s:1,h:0,isnew:1})});change({id:'block:'+host,title:'Запрещено для всех: '+host,detail:'Запрет действует для всех клиентов после применения.',effect:'Ограничивает доступ'})};
 const [helpSlug,setHelpSlug]=useState<string|undefined>();const openHelp=(slug:string)=>{setHelpSlug(slug);setNav('help')};
 const openClient=(id:string,target='overview')=>{setNav('clients');setSelected(id);setTab(target)};
 const openRequests=(id:string,view:View)=>{openClient(id,'requests');setRequestIntent({id:Date.now(),view})};
 const addClient=(name:string,source:string,domains:string[])=>{const id='client-'+Date.now();update(d=>{d.policies.push({id,name,mode:'list',dom:domains.map(x=>({d:x,s:0,h:0}))});d.clients.push({id,name,desc:'',kind:'Устройство',nets:source.split(/[\s,]+/),pol:id,tr:'HTTP',login:'',req:0,den:0,bytes:'—',last:'—',tun:0,on:true,isnew:true})});change({id,client:id,title:'Новый клиент '+name,detail:source,effect:'Расширяет доступ'});openClient(id)};
 const discard=()=>{setData(copy(active));setChanges([]);setConflict(false);setNotice('Черновик сброшен к действующим правилам')};
 const apply=()=>{const next=copy(data);next.clients.forEach(c=>{delete c.changed;delete c.isnew;c.localDomains?.forEach(d=>delete d.isnew)});next.blocked.forEach(d=>delete d.isnew);setData(next);setActive(copy(next));setRevision(r=>r+1);setAudit(a=>[{title:'Применена ревизия r-'+(revision+1),who:manager?'anna':'admin',t:'14:06',day:'Сегодня',type:'apply',sum:'ok',rev:'r-'+(revision+1),ch:changes.map(c=>['m',c.title])},...a]);setChanges([]);setNotice('Применено и подтверждено Squid · r-'+(revision+1))};
 return {data,active,update,changes,setChanges,change,nav,setNav,selected,setSelected,tab,setTab,policy,setPolicy,setting,setSetting,account,setAccount,role,setRole,manager,inScope,env,setEnv,applyFail,setApplyFail,conflict,setConflict,revision,notice,setNotice,review,setReview,marks,mark,allow,allowInPolicy,block,openClient,openRequests,helpSlug,openHelp,addClient,discard,apply,accounts,setAccounts,audit,setAudit,requestIntent,journalIntent,setJournalIntent,sectionIntent,openSection,policyFor:(c:Client)=>policyOf(data,c)};
}
type Store=ReturnType<typeof useDemoStore>;const Context=createContext<Store|null>(null);
export function DemoProvider({children}:{children:ReactNode}){const store=useDemoStore();return <Context value={store}>{children}</Context>}
export const useDemo=()=>useContext(Context)!;
