import {it,expect} from 'vitest';
import {copy,events,defaultFilters,filterEvents,eventCount,formatEventTime,formatRange,compactRange,initialData} from './model';
it('многодневная таблица различает одинаковое время разных дней',()=>{
 const range={from:'2026-10-28T00:00',to:'2026-10-30T23:59'};
 expect(formatEventTime('2026-10-29T14:02:11',range)).toBe('29.10 14:02:11');
 expect(formatEventTime('2026-10-30T14:02:11',range)).toBe('30.10 14:02:11');
 expect(formatEventTime('2026-10-30T14:02:11',{from:'2026-10-30T13:00',to:'2026-10-30T15:00'})).toBe('14:02:11');
 expect(formatRange(range)).toBe('28.10.2026 00:00 — 30.10.2026 23:59 МСК');
});
it('переход из домена выбирает точный host, сохраняя остальные фильтры и счётчики запросов',()=>{
 const e=events.find(e=>e.host==='registry.npmjs.org')!;
 const rows=[{...e,count:5},{...e,id:'sub',host:'sub.registry.npmjs.org',count:2},{...e,id:'other-client',client:'other',count:3}];
 const f={...defaultFilters(),host:e.host,clients:[e.client]};
 const result=filterEvents(rows,f);
 expect(result).toHaveLength(1);expect(eventCount(result)).toBe(5);
});
it('весь веб является режимом клиента, а не пустой общей политикой',()=>{
 const data=copy(initialData);expect(data.policies.some(p=>p.mode==='all')).toBe(false);
 const clients=data.clients.filter(c=>c.access==='all_web');expect(clients.length).toBeGreaterThan(0);expect(clients.every(c=>!c.pol&&!c.policyIDs?.length)).toBe(true);
});

it('компактный период не повторяет дату внутри дня и различает годы',()=>{
 expect(compactRange({from:'2026-10-30T13:00',to:'2026-10-30T14:05'})).toBe('30 окт., 13:00–14:05');
 expect(compactRange({from:'2026-10-29T14:05',to:'2026-10-30T14:05'})).toBe('29 окт., 14:05 — 30 окт., 14:05');
 expect(compactRange({from:'2026-12-31T23:00',to:'2027-01-01T01:00'})).toBe('31 дек. 2026, 23:00 — 1 янв. 2027, 01:00');
});
