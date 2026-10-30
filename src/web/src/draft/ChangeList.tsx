import { Badge } from "@/components/ui/badge";

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
// review sheet and audit detail.
export function ChangeList({ items }: { items: string[] }) {
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((s, i) => (
        <li
          key={i}
          className="grid grid-cols-[22px_minmax(0,1fr)] items-start gap-2.5 border-b py-2.5 last:border-b-0"
        >
          <Badge
            variant={changeKind(s)}
            className="grid size-5 place-items-center px-0 font-mono font-semibold"
          >
            {MARK[changeKind(s)]}
          </Badge>
          <span>{s}</span>
        </li>
      ))}
    </ul>
  );
}
