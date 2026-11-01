import {describe,it,expect} from 'vitest';
import {defaultFilters,filterEvents,groupEvents,type Event} from './model';
const event=(patch:Partial<Event>):Event=>({id:'a',at:'2026-10-30T14:00:00',client:'ci01',ip:'10.20.1.15',host:'example.org',port:'443',method:'CONNECT',status:200,denied:false,bytes:120,count:1,...patch});
describe('Общие представления запросов',()=>{
 it('сворачивает время и порты одного домена, сохраняя результаты и объём',()=>{const groups=groupEvents([event({}),event({id:'b',at:'2026-10-30T13:50:00',port:'80',status:403,denied:true,bytes:0}),event({id:'c',host:'cdn.example.org',ip:'10.20.1.16'})]);expect(groups).toHaveLength(2);expect(groups[0]).toMatchObject({host:'example.org',count:2,denied:1,bytes:120,last:'2026-10-30T14:00:00',sources:1,ports:['443','80']})});
 it('применяет область клиента, результат и диапазон до группировки',()=>{const f={...defaultFilters(),clients:['ci01'],result:'denied' as const,range:{from:'2026-10-30T13:59',to:'2026-10-30T14:00'}};const items=filterEvents([event({denied:true}),event({client:'other',denied:true}),event({at:'2026-10-30T13:58:59',denied:true}),event({at:'2026-10-30T14:01:00',denied:true}),event({})],f);expect(items).toHaveLength(1);expect(groupEvents(items)[0].count).toBe(1)});
 it('объединяет варианты внутри фильтра и пересекает разные фильтры',()=>{const f={...defaultFilters(),clients:['ci01','build'],methods:['GET']};expect(filterEvents([event({}),event({method:'GET'}),event({method:'GET',client:'build'}),event({method:'GET',client:'other'})],f)).toHaveLength(2)});
});
