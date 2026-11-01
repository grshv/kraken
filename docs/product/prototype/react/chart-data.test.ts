import {it,expect} from 'vitest';
import {clientSeries} from './chart-data';
import {events} from './model';
it('график и спарклайн получают 96 интервалов с теми же итогами, что таблица',()=>{const rows=events.filter(e=>e.client==='ci01'),series=clientSeries(rows);expect(series).toHaveLength(96);expect(series.reduce((n,b)=>n+b.requests,0)).toBe(rows.reduce((n,e)=>n+e.count,0));expect(series.reduce((n,b)=>n+b.denied,0)).toBe(rows.filter(e=>e.denied).reduce((n,e)=>n+e.count,0));expect(series.reduce((n,b)=>n+b.bytes,0)).toBe(rows.reduce((n,e)=>n+e.bytes,0))});
it('пустое окно остаётся нулевым; события за его пределами исключены',()=>{expect(clientSeries([]).every(b=>b.requests===0)).toBe(true);expect(clientSeries(events,'2026-10-29T14:15:00+03:00').every(b=>b.requests===0)).toBe(true)});
