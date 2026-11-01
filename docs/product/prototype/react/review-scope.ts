import type {Snapshot} from './reference/model';
import {draftChanges,draftPreview} from './review-data';

function editablePolicies(active:Snapshot,draft:Snapshot,inScope:(id:string)=>boolean) {
  const ids=new Set([...active.policies,...draft.policies].map(p=>p.id));
  return new Set([...ids].filter(id=>{
    const users=[...active.clients,...draft.clients].filter(c=>c.policy_ids.includes(id));
    return users.length>0&&users.every(c=>inScope(c.id));
  }));
}

// Filtering the presentation never grants permission to apply the full draft.
export function scopedReview(active:Snapshot,draft:Snapshot,revision:number,inScope:(id:string)=>boolean) {
  const policies=editablePolicies(active,draft,inScope);
  const all=draftChanges(active,draft);
  const changes=all.filter(c=>c.id&&(c.entity==='client'?inScope(c.id):c.entity==='policy'&&policies.has(c.id)));
  const blocked=changes.length!==all.length;
  const visible=(s:Snapshot):Snapshot=>{
    const clients=s.clients.filter(c=>inScope(c.id));
    return {...s,clients,policies:s.policies.filter(p=>clients.some(c=>c.policy_ids.includes(p.id)))};
  };
  const a=visible(active),d=visible(draft);
  const preview={...draftPreview(a,d,revision),changes,summary:changes.map(c=>c.text),config_diff:[],generated_config:''};
  return {active:a,draft:d,preview,blocked,policies};
}

// An undo in the scoped view preserves every hidden client, shared policy and
// global setting. Only entities editable in the original full draft are merged.
export function mergeScopedReview(active:Snapshot,draft:Snapshot,next:Snapshot,inScope:(id:string)=>boolean):Snapshot {
  const policies=editablePolicies(active,draft,inScope);
  const merge=<T extends {id:string}>(current:T[],next:T[],editable:(id:string)=>boolean)=>{
    const byID=new Map(next.filter(x=>editable(x.id)).map(x=>[x.id,x]));
    return [...current.flatMap(x=>!editable(x.id)?[x]:byID.has(x.id)?[byID.get(x.id)!]:[]),...next.filter(x=>editable(x.id)&&!current.some(old=>old.id===x.id))];
  };
  return {...draft,
    clients:merge(draft.clients,next.clients,inScope),
    policies:merge(draft.policies,next.policies,id=>policies.has(id)),
  };
}
