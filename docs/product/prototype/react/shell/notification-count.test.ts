import {expect, it} from 'vitest';
import {newEventCount} from './notification-count';

const data = {denied: {a: [{d:'npm.example',why:'list'}, {d:'internal.example',why:'port'}], b: [{d:'pip.example',why:'list'}]}, unknown:[{ip:'10.0.0.9'}]};

it('отмеченные события убирают маркер, отказы защиты не создают его', () => {
  expect(newEventCount(data,{},()=>true,true)).toBe(3);
  expect(newEventCount(data,{'a|npm.example':'seen','b|pip.example':'ignored','source/10.0.0.9':'seen'},()=>true,true)).toBe(0);
});

it('маркер не сообщает о событиях вне доступной области', () => {
  expect(newEventCount(data,{},id=>id==='a',false)).toBe(1);
  expect(newEventCount(data,{'a|npm.example':'seen'},id=>id==='a',false)).toBe(0);
});
