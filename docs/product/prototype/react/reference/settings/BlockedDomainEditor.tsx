import {Disclosure} from '@/components/ui/disclosure';
import { useState } from "react";
import { Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { mergeDestinations, parseDomainInput } from "../policies/domains";
import type { Destination } from "../model";

// BlockedDomainEditor is the counterpart of the shared policies/DomainEditor
// for the global "Запрещено для всех" list: a table of domains (Chip,
// per-row subdomain Switch and a remove action), an add row with a "с
// поддоменами" Switch, and the same parse/merge behaviour as the shared editor.
export function BlockedDomainEditor({
  destinations,
  onChange,
}: {
  destinations: Destination[];
  onChange: (next: Destination[]) => void;
}) {
  const [draft, setDraft] = useState("");
  // Default on: the global blocklist matches the prototype's "с поддоменами"
  // add-row switch, so freshly added entries are blocked with their subdomains.
  const [withSubs, setWithSubs] = useState(true);

  function add() {
    const tokens = draft.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);
    const parsed = tokens
      .map((t) => parseDomainInput(t))
      .filter((d): d is NonNullable<typeof d> => d !== null)
      .map((d) => (withSubs ? { ...d, include_subdomains: true } : d));
    if (parsed.length === 0) {
      setDraft("введите домен, например registry.npmjs.org");
      return;
    }
    onChange(mergeDestinations(destinations, parsed));
    setDraft("");
  }

  return (
    <div className="flex flex-col gap-2">
      <Table className="table-fixed">
        <colgroup><col/><col className="w-28"/><col className="w-[60px]"/></colgroup>
        <TableHeader>
          <TableRow>
            <TableHead>Домен</TableHead>
            <TableHead>Поддомены</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {destinations.map((d) => (
            <TableRow key={d.domain}>
              <TableCell>
                <span className="block truncate" title={d.domain}>{d.domain}</span>
              </TableCell>
              <TableCell>
                <Switch
                  checked={d.include_subdomains}
                  aria-label={`Поддомены ${d.domain}`}
                  onCheckedChange={() =>
                    onChange(
                      destinations.map((x) =>
                        x.domain === d.domain
                          ? {
                              ...x,
                              include_subdomains: !x.include_subdomains,
                            }
                          : x,
                      ),
                    )
                  }
                />
              </TableCell>
              <TableCell className="text-right">
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label={`Удалить ${d.domain}`}
                  onClick={() =>
                    onChange(destinations.filter((x) => x.domain !== d.domain))
                  }
                >
                  <X aria-hidden />
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {destinations.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={3}
                className="text-sm text-muted-foreground"
              >
                Нет доменов
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <div className="flex items-center gap-2">
        <Input
          aria-label="Добавить запрещённые домены"
          className="font-mono"
          placeholder="домен; несколько через пробел или с новой строки"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
        />
        <div className="flex items-center gap-1.5 whitespace-nowrap">
          <Switch
            checked={withSubs}
            aria-label="С поддоменами"
            onCheckedChange={setWithSubs}
          />
          <span className="text-sm text-muted-foreground">с поддоменами</span>
        </div>
        <Button variant="outline" onClick={add}>
          <Plus aria-hidden /> Добавить
        </Button>
      </div>
      <Disclosure className="text-sm text-muted-foreground" label="Как добавить список">
        <p className="mt-1">Вставьте домены или ссылки через пробел, запятую или новую строку. Повторы объединяются.</p>
      </Disclosure>
    </div>
  );
}
