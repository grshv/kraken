import { useState } from "react";
import { CalendarIcon } from "lucide-react";
import { format } from "date-fns";
import { ru } from "date-fns/locale";
import type { DateRange, Labels } from "react-day-picker";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
  Field as FormField,
  FieldError,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { MAX_RANGE_MS, type Range } from "./filters";

const calendarLabels: Partial<Labels> = {
  labelNav: () => "Навигация по месяцам",
  labelPrevious: () => "Предыдущий месяц",
  labelNext: () => "Следующий месяц",
  labelDayButton: (date, modifiers) =>
    [
      modifiers.today ? "Сегодня" : null,
      format(date, "PPPP", { locale: ru }),
      modifiers.selected ? "выбрано" : null,
    ]
      .filter(Boolean)
      .join(", "),
};

// Moscow wall clock (UTC+3, no daylight saving) for an instant: the day as a
// local Date for the calendar and the time as "HH:mm".
function moscow(iso: string): { day: Date; time: string } {
  const shifted = new Date(new Date(iso).getTime() + 3 * 3600_000);
  return {
    day: new Date(
      shifted.getUTCFullYear(),
      shifted.getUTCMonth(),
      shifted.getUTCDate(),
    ),
    time: shifted.toISOString().slice(11, 16),
  };
}

// fromMoscow turns a calendar day and "HH:mm" in Moscow time into RFC 3339.
export function fromMoscow(day: Date, time: string): string {
  return new Date(
    `${format(day, "yyyy-MM-dd")}T${time}:00+03:00`,
  ).toISOString();
}

// DateRangePicker: a custom journal period, picked as days and times in
// Moscow time; the server limits the window to 7 days.
export function DateRangePicker({
  value,
  onChange,
}: {
  value: Range;
  onChange: (r: Range) => void;
}) {
  const [open, setOpen] = useState(false);
  const [days, setDays] = useState<DateRange | undefined>();
  const [fromTime, setFromTime] = useState("");
  const [toTime, setToTime] = useState("");
  const [error, setError] = useState("");
  const reset = () => {
    const a = moscow(value.from);
    const b = moscow(value.to);
    setDays({ from: a.day, to: b.day });
    setFromTime(a.time);
    setToTime(b.time);
  };
  const apply = () => {
    if (!days?.from || !days.to) {
      setError("Выберите начало и конец периода");
      return;
    }
    if (!fromTime || !toTime) {
      setError("Укажите время начала и конца");
      return;
    }
    const from = fromMoscow(days.from, fromTime);
    const to = fromMoscow(days.to, toTime);
    const span = Date.parse(to) - Date.parse(from);
    if (!(span > 0)) {
      setError("Конец периода должен быть позже начала");
      return;
    }
    if (span > MAX_RANGE_MS) {
      setError("Период не длиннее 7 суток");
      return;
    }
    onChange({ from, to });
    setOpen(false);
  };
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        setError("");
        if (next) reset();
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="icon-sm"
          className="size-[34px] shrink-0"
          aria-label="Выбрать диапазон дат"
          title="Выбрать диапазон дат"
        >
          <CalendarIcon />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-auto p-0"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <Calendar
          mode="range"
          locale={ru}
          labels={calendarLabels}
          selected={days}
          onSelect={setDays}
          defaultMonth={days?.from}
          numberOfMonths={2}
          showOutsideDays={false}
          disabled={{ after: new Date() }}
          fixedWeeks
          autoFocus
        />
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto_auto] items-end gap-x-2 gap-y-3 border-t p-3">
          <FormField className="min-w-0 gap-1.5">
            <FieldLabel htmlFor="range-from-time" className="text-sm">
              Начало
            </FieldLabel>
            <Input
              id="range-from-time"
              type="time"
              value={fromTime}
              onChange={(e) => setFromTime(e.target.value)}
              className="w-full min-w-0"
            />
          </FormField>
          <FormField className="min-w-0 gap-1.5">
            <FieldLabel htmlFor="range-to-time" className="text-sm">
              Конец
            </FieldLabel>
            <Input
              id="range-to-time"
              type="time"
              value={toTime}
              onChange={(e) => setToTime(e.target.value)}
              className="w-full min-w-0"
            />
          </FormField>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Отмена
          </Button>
          <Button onClick={apply}>Показать</Button>
          <div className="col-span-4 flex min-w-0 items-center justify-between gap-3 text-sm text-muted-foreground">
            <span>
              {days?.from
                ? format(days.from, "d MMM yyyy", { locale: ru })
                : "Выберите начало"}
              {days?.to
                ? " — " + format(days.to, "d MMM yyyy", { locale: ru })
                : " — выберите конец"}
            </span>
            <span className="shrink-0">МСК</span>
          </div>
          {error && (
            <FieldError role="alert" className="col-span-4">
              {error}
            </FieldError>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
