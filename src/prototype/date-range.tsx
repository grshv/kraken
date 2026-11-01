import {useState} from 'react';
import {CalendarIcon} from 'lucide-react';
import {format} from 'date-fns';
import {ru} from 'date-fns/locale';
import type {DateRange,Labels} from 'react-day-picker';
import {Calendar} from '@/components/ui/calendar';
import {Field as FormField,FieldLabel,FieldError} from '@/components/ui/field';
import {Button,Input,Popover,PopoverContent,PopoverTrigger} from './ui';
import type {Range} from './model';
const calendarLabels:Partial<Labels>={
  labelNav:()=> 'Навигация по месяцам',
  labelPrevious:()=> 'Предыдущий месяц',
  labelNext:()=> 'Следующий месяц',
  labelDayButton:(date,modifiers)=>[
    modifiers.today?'Сегодня':null,
    format(date,'PPPP',{locale:ru}),
    modifiers.selected?'выбрано':null,
  ].filter(Boolean).join(', '),
};
export function DateRangePicker({value,onChange}:{value:Range;onChange:(r:Range)=>void}) {
  const [open,setOpen]=useState(false);
  const [days,setDays]=useState<DateRange|undefined>({from:new Date(value.from),to:new Date(value.to)});
  const [fromTime,setFromTime]=useState(value.from.slice(11,16));
  const [toTime,setToTime]=useState(value.to.slice(11,16));
  const [error,setError]=useState('');
  const apply=()=>{
    if(!days?.from||!days.to){setError('Выберите начало и конец периода');return}
    const from=format(days.from,'yyyy-MM-dd')+'T'+fromTime;
    const to=format(days.to,'yyyy-MM-dd')+'T'+toTime;
    if(!fromTime||!toTime||from>=to){setError('Конец периода должен быть позже начала');return}
    onChange({from,to});setOpen(false);
  };
  return <Popover open={open} onOpenChange={next=>{
    setOpen(next);setError('');
    if(next){setDays({from:new Date(value.from),to:new Date(value.to)});setFromTime(value.from.slice(11,16));setToTime(value.to.slice(11,16))}
  }}>
    <PopoverTrigger asChild><Button variant="outline" size="icon-sm" className="size-[34px] shrink-0" aria-label="Выбрать диапазон дат" title="Выбрать диапазон дат"><CalendarIcon/></Button></PopoverTrigger>
    <PopoverContent align="start" className="w-auto p-0" onOpenAutoFocus={e=>e.preventDefault()}>
      <Calendar mode="range" locale={ru} labels={calendarLabels} selected={days} onSelect={setDays} defaultMonth={days?.from} numberOfMonths={2} showOutsideDays={false} fixedWeeks autoFocus/>
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-end gap-x-2 gap-y-3 border-t p-3">
        <FormField className="min-w-0 gap-1.5"><FieldLabel htmlFor="range-from-time" className="text-sm">Начало</FieldLabel><Input id="range-from-time" aria-label="Начальное время" type="time" value={fromTime} onChange={e=>setFromTime(e.target.value)} className="w-full min-w-0"/></FormField>
        <FormField className="min-w-0 gap-1.5"><FieldLabel htmlFor="range-to-time" className="text-sm">Конец</FieldLabel><Input id="range-to-time" aria-label="Конечное время" type="time" value={toTime} onChange={e=>setToTime(e.target.value)} className="w-full min-w-0"/></FormField>
        <Button variant="ghost" onClick={()=>setOpen(false)}>Отмена</Button>
        <Button onClick={apply}>Показать</Button>
        <div className="col-span-4 flex min-w-0 items-center justify-between gap-3 text-sm text-muted-foreground">
          <span>{days?.from?format(days.from,'d MMM yyyy',{locale:ru}):'Выберите начало'}{days?.to?' — '+format(days.to,'d MMM yyyy',{locale:ru}):' — выберите конец'}</span><span className="shrink-0">МСК</span>
        </div>
        {error&&<FieldError role="alert" className="col-span-4">{error}</FieldError>}
      </div>
    </PopoverContent>
  </Popover>;
}
