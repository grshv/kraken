import {expect, it, vi} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import type {ComponentProps} from 'react';
import {DeniedDomains} from './reference/clients/DeniedDomains';
import {TooltipProvider} from './components/ui/tooltip';
import {toSnapshot} from './reference-adapter';
import {copy, initialData} from './model';

const {buttons,menuItems} = vi.hoisted(() => ({buttons: [] as {children?: unknown; onClick?: () => Promise<void>}[],menuItems: [] as {children?: unknown;onSelect?:()=>void}[]}));
vi.mock('@/components/ui/button', async () => {
  const {createElement} = await import('react');
  return {Button: ({variant, size, ...props}: Record<string, unknown>) => {
    buttons.push(props);
    return createElement('button', props);
  }};
});
vi.mock('@/components/ui/dropdown-menu', async () => {
  const {createElement} = await import('react');
  const wrapper=({children}:{children: import('react').ReactNode})=>children;
  return {DropdownMenu:wrapper,DropdownMenuTrigger:wrapper,DropdownMenuContent:wrapper,DropdownMenuSeparator:()=>createElement('hr'),DropdownMenuItem:({children,onSelect}: {children: import('react').ReactNode;onSelect?:()=>void})=>{
    menuItems.push({children,onSelect});return createElement('button',{onClick:onSelect},children);
  }};
});

it('отмена чернового разрешения возвращает новый отказ без скрытой отметки просмотрено', async () => {
  buttons.length = 0;
  const snapshot = toSnapshot(copy(initialData));
  const client = snapshot.clients.find(c => c.id === 'ci01')!;
  const host = 'registry.npmjs.org';
  const onCommit = vi.fn(async () => ({ok: true as const}));
  const onMark = vi.fn(async () => {});
  const props: ComponentProps<typeof DeniedDomains> = {
    client, snap: snapshot, canBlock: true, onCommit, onMark, onToast: () => {},
    items: [{id: 'ci01|' + host, kind: 'denied_domain', status: 'new', client_id: client.id, client_name: client.name, source_ip: null, host, requests: 3, denied: 3, last_seen: '2026-10-30T14:05:00+03:00', marked_by: null, marked_at: null}],
  };
  renderToStaticMarkup(<TooltipProvider><DeniedDomains {...props}/></TooltipProvider>);
  const allow = buttons.find(b => Array.isArray(b.children) && b.children.some(child => typeof child === 'string' && child.trim() === 'Разрешить'));
  expect(allow?.onClick).toBeTypeOf('function');
  await allow!.onClick!();
  expect(onCommit).toHaveBeenCalledOnce();
  expect(onCommit).toHaveBeenCalledWith(expect.objectContaining({clients: expect.arrayContaining([
    expect.objectContaining({id: client.id, destinations: expect.arrayContaining([expect.objectContaining({domain: host})])}),
  ])}));
  expect(onMark).not.toHaveBeenCalled();
  const undo = renderToStaticMarkup(<TooltipProvider><DeniedDomains {...props}/></TooltipProvider>);
  expect(undo).toContain('title="registry.npmjs.org"');
  expect(undo).toContain('aria-label="Выбрать registry.npmjs.org"');
  expect(undo).not.toContain('в черновике');
});

it('отмена чернового глобального запрета не скрывает отказ, явное игнорирование сохраняется', async () => {
  menuItems.length=0;
  const snapshot=toSnapshot(copy(initialData)),client=snapshot.clients.find(c=>c.id==='ci01')!,host='registry.npmjs.org';
  const onCommit=vi.fn(async()=>({ok:true as const})),onMark=vi.fn(async()=>{});
  const props:ComponentProps<typeof DeniedDomains>={client,snap:snapshot,canBlock:true,onCommit,onMark,onToast:()=>{},items:[{id:'ci01|'+host,kind:'denied_domain',status:'new',client_id:client.id,client_name:client.name,source_ip:null,host,requests:3,denied:3,last_seen:'2026-10-30T14:05:00+03:00',marked_by:null,marked_at:null}]};
  renderToStaticMarkup(<TooltipProvider><DeniedDomains {...props}/></TooltipProvider>);
  const item=(label:string)=>menuItems.find(item=>Array.isArray(item.children)&&item.children.some(child=>typeof child==='string'&&child.trim()===label));
  expect(item('Запретить для всех')?.onSelect).toBeTypeOf('function');
  item('Запретить для всех')!.onSelect!();
  await Promise.resolve();
  expect(onCommit).toHaveBeenCalledWith(expect.objectContaining({settings:expect.objectContaining({blocked_domains:expect.arrayContaining([expect.objectContaining({domain:host})])})}));
  expect(onMark).not.toHaveBeenCalled();
  const undone=renderToStaticMarkup(<TooltipProvider><DeniedDomains {...props}/></TooltipProvider>);
  expect(undone).toContain('aria-label="Выбрать registry.npmjs.org"');expect(undone).not.toContain('в черновике');
  item('Игнорировать')!.onSelect!();
  expect(onMark).toHaveBeenCalledWith(['ci01|'+host],'ignored');
});
