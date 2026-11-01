import {expect,it,vi} from 'vitest';
import {renderToStaticMarkup} from 'react-dom/server';
import {ReviewSheet} from './reference/draft/ReviewSheet';
import {draftPreview} from './review-data';
import {toSnapshot} from './reference-adapter';
import {copy,initialData} from './model';
import type {Status} from './reference/model';

// The portal is replaced for SSR; the product state and result content are real.
vi.mock('@/components/ui/sheet',async()=>{
  const {createElement}=await import('react');
  const block=({children}:{children:import('react').ReactNode})=>createElement('div',null,children);
  return {Sheet:block,SheetContent:block,SheetHeader:block,SheetTitle:block,SheetDescription:block,SheetFooter:block};
});

it('успешный результат сохраняет список изменений и не показывает запрет пустого черновика',()=>{
  const active=toSnapshot(copy(initialData)),snapshot=copy(active);
  snapshot.clients.find(c=>c.id==='ci01')!.description='Новое описание';
  const preview=draftPreview(active,snapshot,41);
  const reason='Изменений для применения нет.';
  const html=renderToStaticMarkup(<ReviewSheet open onOpenChange={()=>{}}
    draft={{version:1,snapshot,updated_at:'2026-10-31T19:00:00+03:00'}}
    active={active} preview={preview}
    status={{applied_revision:'r-42',applied_revision_number:42,runtime:{state:'up'},squid:{state:'up'}} as Status}
    run={{state:{phase:'done',step:'apply'},operation:null,error:'',run:async()=>{},reset:()=>{}}}
    applyBlockedReason={reason} onRefreshDraft={()=>{}}/>);
  expect(html).toContain('r-42');
  expect(html).toContain(preview.changes![0].text);
  expect(html).not.toContain(reason);
});
