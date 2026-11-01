import {useCallback,useMemo,useState} from 'react';
import {useDemo} from './store';
import {toSnapshot,updateData} from './reference-adapter';
import {events} from './model';
import {draftChanges,draftImpact,draftPreview} from './review-data';
import {mergeScopedReview,scopedReview} from './review-scope';
import {ReviewSheet} from './reference/draft/ReviewSheet';
import type {ApplyRun} from './reference/draft/useApplyFlow';
import type {ApplyState} from './reference/draft/applyFlow';
import type {Status} from './reference/model';
import type {Request} from './reference/api';

export function Review(){
  const s=useDemo();
  const [state,setState]=useState<ApplyState>({phase:'idle'});
  const snapshot=useMemo(()=>toSnapshot(s.data),[s.data]);
  const active=useMemo(()=>toSnapshot(s.active),[s.active]);
  const scope=useMemo(()=>s.manager?scopedReview(active,snapshot,s.revision,s.inScope):null,[active,snapshot,s.revision,s.manager]);
  const visibleDraft=scope?.draft??snapshot,visibleActive=scope?.active??active;
  const preview=useMemo(()=>scope?.preview??draftPreview(active,snapshot,s.revision),[scope,active,snapshot,s.revision]);
  const applyBlockedReason=scope?.blocked
    ?'В черновике есть изменения вне вашей области. Администратор должен применить или отменить их.'
    :!preview.changes?.length?'Изменений для применения нет.':undefined;
  const component={state:'up' as const,detail:'',observed_at:null,stale:false};
  const status:Status={has_unapplied_changes:!!s.changes.length,runtime_update_available:false,web:component,runtime:component,squid:{...component,state:s.env==='ok'?'up':'down'},applied_revision:'r-'+s.revision,applied_revision_number:s.revision,observed_config_hash:null,capabilities:[],instance:{name:'proxy-01',data_free_bytes:null,data_total_bytes:null,journal_last_event_at:null,journal_checkpoint_at:null,last_applied_at:null}};
  const request=useCallback<Request>(async <T,>(path:string):Promise<T>=>{
    if(path==='/configuration/preview')return preview as T;
    if(path==='/inspector/draft-impact')return draftImpact(visibleActive,visibleDraft,events.filter(e=>!s.manager||s.inScope(e.client))) as T;
    throw Error('Нет демонстрационного ответа: '+path);
  },[visibleActive,visibleDraft,preview,s.manager]);
  const run:ApplyRun={state,operation:null,error:'',reset:()=>setState({phase:'idle'}),run:async()=>{
    if(applyBlockedReason){setState({phase:'failed',code:scope?.blocked?'draft_out_of_scope':'no_changes'});return;}
    if(s.conflict){setState({phase:'conflict'});return;}
    if(s.applyFail){setState({phase:'rolled_back',step:'apply'});return;}
    setState({phase:'done',step:'apply'});s.apply();
  }};
  return <ReviewSheet open={s.review} onOpenChange={s.setReview}
    draft={{version:s.changes.length+1,snapshot:visibleDraft,updated_at:'2026-10-30T14:05:00+03:00'}}
    preview={preview} status={status} request={request} run={run} applyBlockedReason={applyBlockedReason}
    onRefreshDraft={()=>{s.setConflict(false);setState({phase:'idle'});}}
    onOpenAudit={()=>{s.setReview(false);s.setNav('audit');}} active={visibleActive}
    onCommit={async next=>{
      const merged=s.manager?mergeScopedReview(active,snapshot,next,s.inScope):next;
      s.update(d=>updateData(d,merged));
      const changes=draftChanges(active,merged);
      s.setChanges(changes.map(c=>({id:c.entity+':'+c.id,client:c.entity==='client'?c.id||undefined:undefined,title:c.text,detail:'Изменение в черновике',effect:'Меняет настройки'})));
      if(!changes.length)s.setReview(false);
      return {ok:true};
    }}/>
}
