import {expect, it} from 'vitest';
import {loadPolicySuggestions,policySuggestions} from './reference/policies/suggestions';
import type {ApiRequest} from './reference/model';
import type {Notice} from './reference/clients/notifications';
import {toSnapshot} from './reference-adapter';
import {copy, initialData} from './model';

const notice = (id:string, host='registry.npmjs.org'):Notice => ({id:id+'|'+host,kind:'denied_domain',status:'new',client_id:id,client_name:null,source_ip:null,host,requests:3,denied:3,last_seen:'2026-10-30T14:05:00+03:00',marked_by:null,marked_at:null});
it('пустой ответ уведомлений отличается от сбоя и повреждённого ответа',async()=>{
  const snap=toSnapshot(copy(initialData));
  const failure=new Error('connection failed');
  await expect(loadPolicySuggestions((async()=>{throw failure;}) as ApiRequest,snap,snap.clients)).rejects.toBe(failure);
  await expect(loadPolicySuggestions((async()=>({})) as ApiRequest,snap,snap.clients)).rejects.toThrow('Не удалось прочитать уведомления');
  await expect(loadPolicySuggestions((async()=>({items:[]})) as ApiRequest,snap,snap.clients)).resolves.toEqual([]);
});
it('загружает причины из уведомлений, а не из общего агрегата отказов',async()=>{
  const snap=toSnapshot(copy(initialData));let path='';
  await loadPolicySuggestions((async(p:string)=>{path=p;return {items:[]};}) as ApiRequest,snap,snap.clients);
  expect(path).toBe('/notifications?status=all&kind=denied_domain');
});
it('предлагает только уведомления об отказе списка в доступной области',()=>{
  const snap=toSnapshot(copy(initialData)), c=snap.clients.find(c=>c.id==='ci01')!;
  const rows=policySuggestions([notice(c.id),notice('foreign'),{...notice(c.id),kind:'unknown_source'},{...notice(c.id),status:'ignored'}],snap,[c]);
  expect(rows).toEqual([{host:'registry.npmjs.org',denied:3,client:c.name,clientID:c.id}]);
});
it('черновое разрешение и общий запрет исключают предложение, отмена возвращает его',()=>{
  const snap=toSnapshot(copy(initialData)), c=snap.clients.find(c=>c.id==='ci01')!, n=notice(c.id);
  expect(policySuggestions([n],snap,[{...c,destinations:[{domain:n.host!,include_subdomains:false}]}])).toEqual([]);
  expect(policySuggestions([n],{...snap,settings:{...snap.settings!,blocked_domains:[{domain:'npmjs.org',include_subdomains:true}]}},[c])).toEqual([]);
  expect(policySuggestions([n],snap,[c])).toHaveLength(1);
});
it('не предлагает расширять доступ выключенного клиента, весь веб, отсутствие доступа и expert',()=>{
  const snap=toSnapshot(copy(initialData)), c=snap.clients.find(c=>c.id==='ci01')!, n=notice(c.id);
  for(const client of [{...c,enabled:false},{...c,access:'all_web' as const},{...c,access:'none' as const}]) expect(policySuggestions([n],snap,[client])).toEqual([]);
  expect(policySuggestions([n],{...snap,configuration_mode:'expert'},[c])).toEqual([]);
});
