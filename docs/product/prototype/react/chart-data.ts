import type {Event} from './model';
import type {Bucket} from './reference/clients/TrafficChart';
// The same demo events feed the tables, totals and charts. Times in the mock
// are Moscow wall-clock strings; make the offset explicit before bucketing.
export function clientSeries(items:Event[],end='2026-10-30T14:15:00+03:00'):Bucket[]{const step=15*60*1000,last=Date.parse(end),first=last-96*step;const buckets=Array.from({length:96},(_,i)=>({start:new Date(first+i*step).toISOString(),requests:0,denied:0,bytes:0}));for(const e of items){const timestamp=Date.parse(e.at+'+03:00'),i=Math.floor((timestamp-first)/step);if(i<0||i>=buckets.length)continue;const b=buckets[i];b.requests+=e.count;b.denied+=e.denied?e.count:0;b.bytes+=e.bytes}return buckets}
