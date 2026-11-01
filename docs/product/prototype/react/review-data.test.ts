import {it,expect} from 'vitest';
import {initialData,copy,events} from './model';
import {toSnapshot} from './reference-adapter';
import {draftChanges,draftImpact,draftPreview} from './review-data';
import {revertChange} from './reference/draft/revert';
it('передаёт структурированные изменения клиента и настроек для пообъектной отмены',()=>{const base=toSnapshot(copy(initialData)),draft=copy(base);draft.clients.find(c=>c.id==='ci01')!.name='ci-renamed';draft.settings!.blocked_domains!.push({domain:'pypi.org',include_subdomains:false});const changes=draftChanges(base,draft);expect(changes.map(c=>[c.entity,c.id,c.action])).toEqual([['client','ci01','mod'],['settings',null,'mod']]);const next=revertChange(draft,base,changes[0]);expect(next.clients).toEqual(base.clients);expect(draftChanges(base,next).map(c=>c.entity)).toEqual(['settings']);expect(draftChanges(base,revertChange(next,base,changes[1]))).toEqual([])});
it('влияние и фрагменты меняются при добавлении и отмене разрешения',()=>{const active=toSnapshot(copy(initialData)),draft=copy(active);draft.clients.find(c=>c.id==='ci01')!.destinations.push({domain:'registry.npmjs.org',include_subdomains:false});expect(draftImpact(active,draft,events).newly_allowed).toBe(2);const preview=draftPreview(active,draft,41);expect(preview.changes![0].id).toBe('ci01');expect(preview.config_diff.some(l=>l.kind==='added'&&l.text.includes('registry.npmjs.org'))).toBe(true);expect(draftChanges(active,revertChange(draft,active,preview.changes![0]))).toEqual([])});
it('перестановка политик и доменов не создаёт ложных изменений',()=>{const a=toSnapshot(copy(initialData)),b=copy(a);b.policies.reverse();b.policies.forEach(p=>p.destinations.reverse());expect(draftChanges(a,b)).toEqual([])});
it('объясняет изменение подключения, своих доменов и запретов без чтения ACL',()=>{
 const active=toSnapshot(copy(initialData)),draft=copy(active),client=draft.clients.find(c=>c.id==='ci01')!;
 client.tls_profile_id='tls';client.proxy_username='ci-user';client.destinations.push({domain:'registry.npmjs.org',include_subdomains:false});
 draft.settings!.blocked_domains!.push({domain:'blocked.example',include_subdomains:true});
 const changes=draftChanges(active,draft);
 expect(changes.find(c=>c.entity==='client')!.details).toEqual(expect.arrayContaining(['Подключение: HTTP → TLS','Логин прокси: — → ci-user','Свои домены: добавлено registry.npmjs.org']));
 expect(changes.find(c=>c.entity==='settings')!.details).toContain('Запрещено для всех: добавлено *.blocked.example');
});
it('показывает контекст удалённых объектов и сохраняет их пообъектную отмену',()=>{
 const active=toSnapshot(copy(initialData)),draft=copy(active),client=active.clients.find(c=>c.id==='ci01')!;
 const policy=active.policies.find(p=>p.id===client.policy_ids[0])!;
 draft.clients=draft.clients.filter(c=>c.id!==client.id);draft.policies=draft.policies.filter(p=>p.id!==policy.id);
 const changes=draftChanges(active,draft),removedClient=changes.find(c=>c.entity==='client')!,removedPolicy=changes.find(c=>c.entity==='policy')!;
 expect(removedClient.details).toContain('Источники: '+client.source_networks.join(', '));
 expect(removedClient.details).toContain('Политики: '+policy.name);
 expect(removedPolicy.details!.some(line=>line.includes('pypi.org'))).toBe(true);
 expect(removedPolicy.details).toContain('Подключена у: '+client.name);
 const restored=revertChange(revertChange(draft,active,removedPolicy),active,removedClient);
 expect(draftChanges(active,restored)).toEqual([]);
});
it('объясняет добавление и удаление TLS-профилей человеческими строками',()=>{
 const active=toSnapshot(copy(initialData)),draft=copy(active);
 draft.tls_profiles=draft.tls_profiles.filter(p=>p.id!=='tls');
 draft.tls_profiles.push({id:'office',name:'Офис',mode:'tunnel',proxy_transport:'tls',cert_ref:'office-cert'});
 const details=draftChanges(active,draft).find(c=>c.entity==='tls_profiles')!.details!;
 expect(details).toContain('Добавлен профиль «Офис»: подключение TLS, режим Сквозной CONNECT, сертификат office-cert');
 expect(details).toContain('Удалён профиль «TLS»: подключение TLS, режим Сквозной CONNECT, сертификат proxy-cert');
 expect(details.join(' ')).not.toMatch(/proxy_transport|cert_ref|\{"/);
});
it('показывает конкретные изменения имени, транспорта, режима и сертификата профиля',()=>{
 const active=toSnapshot(copy(initialData)),draft=copy(active),profile=draft.tls_profiles.find(p=>p.id==='tls')!;
 profile.name='Новый профиль';profile.proxy_transport='http';profile.mode='custom';profile.cert_ref=null;
 const details=draftChanges(active,draft).find(c=>c.entity==='tls_profiles')!.details!;
 expect(details).toEqual(expect.arrayContaining([
  'Профиль «TLS»: Имя: TLS → Новый профиль',
  'Профиль «TLS»: Подключение: TLS → HTTP',
  'Профиль «TLS»: Режим: Сквозной CONNECT → custom',
  'Профиль «TLS»: Сертификат: proxy-cert → не задан',
 ]));
 const restored=copy(draft);restored.tls_profiles=copy(active.tls_profiles);
 expect(draftChanges(active,restored)).toEqual([]);
});
