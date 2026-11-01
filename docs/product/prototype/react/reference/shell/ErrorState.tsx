import {AlertTriangle} from 'lucide-react';
import {Button} from '@/components/ui/button';
import {Alert,AlertTitle,AlertDescription} from '@/components/ui/alert';
export function ErrorState({title,detail,onRetry}:{title:string;detail?:string;onRetry?:()=>void}){return <Alert variant="destructive" className="max-w-[460px]"><AlertTriangle/><AlertTitle>{title}</AlertTitle><AlertDescription>{detail}{onRetry&&<Button className="mt-3" onClick={onRetry}>Повторить</Button>}</AlertDescription></Alert>}
