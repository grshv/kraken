import {expect,it} from 'vitest';
import {copy,initialData} from './model';
import {toSnapshot} from './reference-adapter';
import {mergeScopedReview,scopedReview} from './review-scope';
const scope=(id:string)=>id==='ci01'||id==='build';
it('свою отдельную политику можно просмотреть и применить',()=>{
  const active=toSnapshot(copy(initialData)),draft=copy(active);
  const id=draft.clients.find(c=>c.id==='ci01')!.policy_ids[0];
  draft.policies.find(p=>p.id===id)!.destinations.push({domain:'own.example',include_subdomains:false});
  const result=scopedReview(active,draft,41,scope);
  expect(result.blocked).toBe(false);
  expect(result.preview.changes?.map(c=>[c.entity,c.id])).toEqual([['policy',id]]);
});
it('одни глобальные настройки и смена экспертного режима тоже блокируют применение',()=>{
  const active=toSnapshot(copy(initialData)),settings=copy(active),expert=copy(active);
  settings.settings!.blocked_domains!.push({domain:'private.example',include_subdomains:false});
  expert.configuration_mode='expert';
  for(const draft of [settings,expert]){
    const result=scopedReview(active,draft,41,scope);
    expect(result.blocked).toBe(true);
    expect(result.preview.changes).toEqual([]);
    expect(result.preview.config_diff).toEqual([]);
  }
});
it('чужие изменения не показываются и блокируют применение всего черновика',()=>{
  const active=toSnapshot(copy(initialData)),draft=copy(active);
  draft.clients.find(c=>c.id==='ci01')!.description='своё изменение';
  draft.clients.find(c=>c.id==='dev')!.description='чужое изменение';
  draft.settings!.blocked_domains!.push({domain:'secret.example',include_subdomains:false});
  const result=scopedReview(active,draft,41,scope);
  expect(result.blocked).toBe(true);
  expect(result.preview.changes?.map(c=>c.id)).toEqual(['ci01']);
  expect(result.preview.config_diff).toEqual([]);
  expect(result.draft.clients.every(c=>scope(c.id))).toBe(true);
});
it('свой черновик можно применять; общую с чужим клиентом политику нельзя',()=>{
  const active=toSnapshot(copy(initialData)),draft=copy(active);
  draft.clients.find(c=>c.id==='ci01')!.description='своё изменение';
  expect(scopedReview(active,draft,41,scope).blocked).toBe(false);
  const id=draft.clients.find(c=>c.id==='build')!.policy_ids[0];
  draft.policies.find(p=>p.id===id)!.destinations.push({domain:'added.example',include_subdomains:false});
  const result=scopedReview(active,draft,41,scope);
  expect(result.blocked).toBe(true);
  expect(result.preview.changes?.some(c=>c.entity==='policy')).toBe(false);
});
it('отмена своего изменения сохраняет чужой черновик и глобальные настройки',()=>{
  const active=toSnapshot(copy(initialData)),draft=copy(active);
  draft.clients.find(c=>c.id==='dev')!.description='сохранить';
  draft.clients.find(c=>c.id==='ci01')!.description='отменить';
  draft.settings!.blocked_domains!.push({domain:'blocked.example',include_subdomains:false});
  const visible=scopedReview(active,draft,41,scope).draft;
  visible.clients=visible.clients.map(c=>c.id==='ci01'?active.clients.find(a=>a.id===c.id)!:c);
  const merged=mergeScopedReview(active,draft,visible,scope);
  expect(merged.clients.find(c=>c.id==='ci01')).toEqual(active.clients.find(c=>c.id==='ci01'));
  expect(merged.clients.find(c=>c.id==='dev')).toEqual(draft.clients.find(c=>c.id==='dev'));
  expect(merged.settings).toEqual(draft.settings);
  expect(merged.policies).toEqual(draft.policies);
});
