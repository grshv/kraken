import type {Change,Snapshot,Preview,Client,Destination,Settings} from './reference/model';
import {effectiveDestinations} from './reference/draft/edits';
import type {Event} from './model';
import type {DraftImpact} from './reference/draft/ReviewSheet';
const canonical=(value:unknown):string=>JSON.stringify(value,(_key,v)=>v&&typeof v==='object'&&!Array.isArray(v)?Object.fromEntries(Object.entries(v).sort(([a],[b])=>a.localeCompare(b))):Array.isArray(v)?[...v].sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b))):v);
const changedName=(before:unknown,after:unknown)=>changed('Имя',before,after);
const accessLabel={list:'По списку',all_web:'Весь веб',none:'Нет доступа'};
const destination=(d:Destination)=>(d.include_subdomains?'*.':'')+d.domain;
function listChanges(label:string,before:string[]=[],after:string[]=[]):string[]{const removed=before.filter(v=>!after.includes(v)),added=after.filter(v=>!before.includes(v));return [...(added.length?[label+': добавлено '+added.join(', ')]:[]),...(removed.length?[label+': удалено '+removed.join(', ')]:[])]}
function changed(label:string,before:unknown,after:unknown):string[]{const show=(v:unknown)=>v===true?'Включено':v===false?'Выключено':Array.isArray(v)?v.join(', ')||'—':String(v??'')||'—';return canonical(before)===canonical(after)?[]:[label+': '+show(before)+' → '+show(after)]}
function clientDetails(before:Client|undefined,after:Client,snapshot:Snapshot,active:Snapshot):string[]{const profile=(s:Snapshot,id:string)=>s.tls_profiles.find(p=>p.id===id)?.proxy_transport==='tls'?'TLS':'HTTP';return [
...changed('Имя',before?.name,after.name),...changed('Описание',before?.description,after.description),
...changed('Клиент включён',before?.enabled,after.enabled),
...changed('Доступ',before?accessLabel[before.access]:'—',accessLabel[after.access]),
// The widest mode gets its own line so it is not lost among the details.
...(after.access==='all_web'&&before?.access!=='all_web'?['Открывает весь веб: любые внешние сайты, кроме запрещённых для всех']:[]),
...changed('Подключение',before?profile(active,before.tls_profile_id):'—',profile(snapshot,after.tls_profile_id)),
...changed('Логин прокси',before?.proxy_username,after.proxy_username),
...listChanges('Источники',before?.source_networks,after.source_networks),
...listChanges('Политики',before?.policy_ids.map(id=>active.policies.find(p=>p.id===id)?.name||id),after.policy_ids.map(id=>snapshot.policies.find(p=>p.id===id)?.name||id)),
...listChanges('Свои домены',before?.destinations.map(destination),after.destinations.map(destination)),
]}
function removedClientDetails(client:Client,active:Snapshot):string[]{return [
  'Источники: '+(client.source_networks.join(', ')||'—'),
  'Доступ: '+accessLabel[client.access],
  'Подключение: '+(active.tls_profiles.find(p=>p.id===client.tls_profile_id)?.proxy_transport==='tls'?'TLS':'HTTP'),
  'Политики: '+(client.policy_ids.map(id=>active.policies.find(p=>p.id===id)?.name||id).join(', ')||'—'),
  'Свои домены: '+(client.destinations.map(destination).join(', ')||'—'),
]}
function tlsProfileDetails(active:Snapshot,draft:Snapshot):string[]{
  type Profile=Snapshot['tls_profiles'][number];
  const transport=(value:string)=>value==='tls'?'TLS':value==='http'?'HTTP':value;
  const mode=(value:string)=>value==='tunnel'?'Сквозной CONNECT':value;
  const context=(p:Profile)=>`подключение ${transport(p.proxy_transport)}, режим ${mode(p.mode)}, сертификат ${p.cert_ref||'не задан'}`;
  const details:string[]=[];
  for(const profile of draft.tls_profiles){
    const before=active.tls_profiles.find(p=>p.id===profile.id);
    if(!before){details.push(`Добавлен профиль «${profile.name}»: ${context(profile)}`);continue}
    const changes=[...changed('Имя',before.name,profile.name),...changed('Подключение',transport(before.proxy_transport),transport(profile.proxy_transport)),...changed('Режим',mode(before.mode),mode(profile.mode)),...changed('Сертификат',before.cert_ref||'не задан',profile.cert_ref||'не задан')];
    details.push(...changes.map(line=>`Профиль «${before.name}»: ${line}`));
  }
  for(const profile of active.tls_profiles)if(!draft.tls_profiles.some(p=>p.id===profile.id))details.push(`Удалён профиль «${profile.name}»: ${context(profile)}`);
  return details;
}
const settingLabels:Record<keyof Settings,string>={http_port:'Порт HTTP',tls_port:'Порт TLS',safe_ports:'Порты сайтов HTTP',connect_ports:'Порты HTTPS',cache_enabled:'Кеш в памяти',cache_memory_mb:'Объём кеша, МБ',maximum_object_kb:'Размер объекта, КБ',dns_nameservers:'DNS-серверы',positive_dns_ttl_seconds:'TTL DNS, с',negative_dns_ttl_seconds:'TTL отрицательных ответов, с',connect_timeout_seconds:'Ожидание подключения, с',read_timeout_seconds:'Ожидание чтения, с',request_timeout_seconds:'Время запроса, с',max_request_body_mb:'Тело запроса, МБ',log_rotations:'Ротации access.log',allow_private_destinations:'Доступ к внутренним адресам',blocked_domains:'Запрещённые домены',hide_client_address:'Скрывать адрес клиента',upstream_proxy:'Вышестоящий прокси'};
function settingsDetails(before:Settings|undefined,after:Settings|undefined):string[]{if(!after)return ['Настройки удалены'];return (Object.keys(settingLabels) as (keyof Settings)[]).flatMap(key=>{
if(key==='blocked_domains')return listChanges('Запрещено для всех',before?.blocked_domains?.map(destination),after.blocked_domains?.map(destination));
if(key==='upstream_proxy'){const peer=(v:Settings['upstream_proxy'])=>v?v.address+':'+v.port+(v.exclusive?' · без прямого подключения':''):'Выключен';return changed(settingLabels[key],peer(before?.[key]),peer(after[key]))}
return changed(settingLabels[key],before?.[key],after[key]);
})}
// Same entity/action/text contract as semanticChangesAll in configuration.go.
export function draftChanges(active:Snapshot,draft:Snapshot):Change[]{const out:Change[]=[];for(const [entity,old,now,added,changed,removed] of [
 ['client',active.clients,draft.clients,'Добавлен клиент: ','Изменён клиент: ','Удалён клиент: '],
 ['policy',active.policies,draft.policies,'Добавлена политика: ','Изменена политика: ','Удалена политика: '],
] as const){for(const item of now){const before=old.find(x=>x.id===item.id);if(!before||canonical(before)!==canonical(item))out.push({entity,id:item.id,action:before?'mod':'add',text:(before?changed:added)+item.name,details:entity==='client'?clientDetails(before as Client|undefined,item as Client,draft,active):[...changedName(before?.name,item.name),...listChanges('Домены',(before as {destinations:Destination[]}|undefined)?.destinations.map(destination),item.destinations.map(destination))]})}for(const item of old)if(!now.some(x=>x.id===item.id))out.push({entity,id:item.id,action:'del',text:removed+item.name,details:entity==='client'?removedClientDetails(item as Client,active):['Домены: '+(item.destinations.map(destination).join(', ')||'—'),'Подключена у: '+(active.clients.filter(c=>c.policy_ids.includes(item.id)).map(c=>c.name).join(', ')||'—')]})}
if(canonical(active.settings)!==canonical(draft.settings)){const blocked=canonical(active.settings?.blocked_domains??[])!==canonical(draft.settings?.blocked_domains??[]),rest=canonical({...active.settings,blocked_domains:[]})!==canonical({...draft.settings,blocked_domains:[]});
 // The global deny list is an access rule, not a Squid setting.
 out.push({entity:'settings',id:null,action:'mod',text:blocked&&!rest?'Изменено правило «Запрещено для всех»':blocked?'Изменены «Запрещено для всех» и настройки Squid':'Изменены настройки Squid',details:settingsDetails(active.settings,draft.settings)})}
if(active.configuration_mode!==draft.configuration_mode||active.expert_config!==draft.expert_config)out.push({entity:'configuration',id:null,action:'mod',text:'Изменён режим конфигурации или экспертный squid.conf',details:[...changed('Режим',active.configuration_mode==='expert'?'Вручную':'Визуальные настройки',draft.configuration_mode==='expert'?'Вручную':'Визуальные настройки'),...(active.expert_config!==draft.expert_config?['Изменён текст squid.conf']:[])]});
if(canonical(active.tls_profiles)!==canonical(draft.tls_profiles))out.push({entity:'tls_profiles',id:null,action:'mod',text:'Изменены TLS-профили',details:tlsProfileDetails(active,draft)});return out}
const domain=(host:string,d:{domain:string;include_subdomains:boolean})=>host===d.domain||d.include_subdomains&&host.endsWith('.'+d.domain);
function allows(s:Snapshot,e:Event):boolean{const c=s.clients.find(c=>c.id===e.client),settings=s.settings;if(!c?.enabled||c.access==='none')return false;if(settings&&!settings.safe_ports.includes(+e.port))return false;if(e.method==='CONNECT'&&settings&&!settings.connect_ports.includes(+e.port))return false;if(settings?.blocked_domains?.some(d=>domain(e.host,d)))return false;return c.access==='all_web'||effectiveDestinations(s,c).some(x=>domain(e.host,x.destination))}
export function draftImpact(active:Snapshot,draft:Snapshot,events:Event[]):DraftImpact{const clients=new Map<string,{client_id:string;newly_allowed:number;newly_denied:number}>();for(const e of events){const before=allows(active,e),after=allows(draft,e);if(before===after||e.client==='?')continue;const c=clients.get(e.client)||{client_id:e.client,newly_allowed:0,newly_denied:0};c[after?'newly_allowed':'newly_denied']+=e.count;clients.set(e.client,c)}const rows=[...clients.values()];return {window_hours:24,newly_allowed:rows.reduce((n,c)=>n+c.newly_allowed,0),newly_denied:rows.reduce((n,c)=>n+c.newly_denied,0),clients:rows}}
// Only the managed client/domain/port ACL fragments are projected here. This
// is demo preview data, never a replacement for the Go renderer or applied.
export function aclFragments(s:Snapshot):string[]{const out:string[]=[],settings=s.settings;if(settings){out.push('acl sc_safe_ports port '+settings.safe_ports.join(' '),'acl sc_ssl_ports port '+settings.connect_ports.join(' '));if(settings.blocked_domains?.length)out.push('acl sc_blocked_dst dstdomain -n '+settings.blocked_domains.map(d=>(d.include_subdomains?'.':'')+d.domain).sort().join(' '),'http_access deny sc_blocked_dst sc_r_blocked')}
const auth=[...new Set(s.clients.filter(c=>c.enabled&&c.proxy_username).flatMap(c=>c.source_networks))].sort();if(auth.length)out.push('acl sc_auth_sources src '+auth.join(' '));out.push('# BEGIN user policy');if(s.configuration_mode==='expert')out.push(...(s.expert_config||'').split('\n'));else [...s.clients].sort((a,b)=>a.id.localeCompare(b.id)).forEach((c,i)=>{if(!c.enabled)return;out.push(`acl client_${i} src ${c.source_networks.join(' ')}`);const listener=s.tls_profiles.find(p=>p.id===c.tls_profile_id)?.proxy_transport==='tls'?'sc_tls_listener':'sc_http_listener';let auth='';if(c.proxy_username){out.push(`acl sc_proxy_user_${i} proxy_auth_regex ^${c.proxy_username.replaceAll('.','[.]')}$`);auth=` sc_proxy_user_${i} sc_proxy_current`}if(c.access==='all_web'){out.push(`http_access allow client_${i} ${listener}${auth}`);return}const destinations=effectiveDestinations(s,c).map(x=>(x.destination.include_subdomains?'.':'')+x.destination.domain).sort();if(c.access==='list'&&destinations.length)out.push(`acl destinations_${i} dstdomain -n ${destinations.join(' ')}`,`http_access allow client_${i} ${listener} destinations_${i}${auth}`)});out.push('# END user policy');return out}
function lineDiff(a:string[],b:string[]):Preview['config_diff']{const counts=Array.from({length:a.length+1},()=>new Uint16Array(b.length+1));for(let i=a.length-1;i>=0;i--)for(let j=b.length-1;j>=0;j--)counts[i][j]=a[i]===b[j]?1+counts[i+1][j+1]:Math.max(counts[i+1][j],counts[i][j+1]);const out:Preview['config_diff']=[];let i=0,j=0;while(i<a.length||j<b.length){if(i<a.length&&j<b.length&&a[i]===b[j]){out.push({kind:'context',text:a[i++]});j++}else if(i<a.length&&(j===b.length||counts[i+1][j]>=counts[i][j+1]))out.push({kind:'removed',text:a[i++]});else out.push({kind:'added',text:b[j++]})}return out}
export function draftPreview(active:Snapshot,draft:Snapshot,revision:number):Preview{const changes=draftChanges(active,draft),diff=lineDiff(aclFragments(active),aclFragments(draft));return {user_changes:!!changes.length,runtime_changes:diff.some(l=>l.kind!=='context'),summary:changes.map(c=>c.text),changes,config_diff:diff,policy_hash:'mock',generated_config:'',warnings:[],expected_applied_revision:'r-'+revision}}
