import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// changeKind picks the change marker from a summary line of semanticDiff or an
// audit detail ("Добавлен клиент: x", "Политика «x» удалена"; anything else is
// a modification).
export function changeKind(line: string): "add" | "del" | "mod" {
  if (/(^|\s)добавлен[аоы]?(\s|$|:)/iu.test(line)) return "add";
  if (/(^|\s)удал[её]н[аоы]?(\s|$|:)/iu.test(line)) return "del";
  return "mod";
}

const MARK = { add: "+", del: "−", mod: "~" } as const;

// ChangeList renders change lines with a +/−/~ marker, as in the prototype's
// review sheet and audit detail. When onUndo is provided, undoable rows get a
// "Отменить" button and the grid gains a third auto column.
export function ChangeList({
  items,
  onUndo,
  undoable,
  details,
}: {
  items: string[];
  onUndo?: (index: number) => void;
  undoable?: boolean[];
  details?: (string[] | undefined)[];
}) {
  const hasUndo = !!onUndo;
  const cols = hasUndo
    ? "grid-cols-[22px_minmax(0,1fr)_auto]"
    : "grid-cols-[22px_minmax(0,1fr)]";
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((s, i) => (
        <li
          key={i}
          className={`grid ${cols} items-start gap-2.5 border-b py-2.5 last:border-b-0`}
        >
          <Badge
            variant={changeKind(s)}
            className="grid size-5 place-items-center px-0 font-mono font-semibold"
          >
            {MARK[changeKind(s)]}
          </Badge>
          <div className="min-w-0"><span>{s}</span>{!!details?.[i]?.length&&<ul className="mt-1 space-y-1 text-sm text-muted-foreground [overflow-wrap:anywhere]">{details[i]!.map(line=><li key={line}>{line}</li>)}</ul>}</div>
          {hasUndo && undoable?.[i] && (
            <Button
              size="xs"
              variant="ghost"
              aria-label={`Отменить: ${s}`}
              onClick={() => onUndo!(i)}
            >
              Отменить
            </Button>
          )}
        </li>
      ))}
    </ul>
  );
}
