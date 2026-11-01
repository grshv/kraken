import {ago,dateTime,localTime} from '@/reference/model';
import {Tooltip,TooltipContent,TooltipTrigger} from '../ui/tooltip';

// One display policy for compact rows, exact diagnostics and relative activity.
export function Timestamp({value,mode='datetime',seconds=true,compact=false}:{value:string|null|undefined;mode?:'datetime'|'time'|'relative';seconds?:boolean;compact?:boolean}) {
  const exact=dateTime(value);
  if(!value||exact==='—')return <span>—</span>;
  const text=mode==='relative'?ago(value):mode==='time'?localTime(value,seconds):dateTime(value,seconds,compact);
  return <Tooltip><TooltipTrigger asChild><time dateTime={value} title={exact+' МСК'} aria-label={exact+' МСК'} className="tabular-nums">{text}</time></TooltipTrigger><TooltipContent>{exact} МСК</TooltipContent></Tooltip>;
}
