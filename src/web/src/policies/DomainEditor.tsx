import { Label } from "@/components/ui/label";
import { Disclosure } from "@/components/kraken/disclosure";
import { useState } from "react";
import { Lock, Plus, Undo, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Callout } from "@/components/kraken/blocks";
import type { Destination } from "../model";
import { mergeDestinations, parseDomainInput } from "../policies/domains";

// Hit counts per domain (24h / 7d) from /inspector/policies/{id}/hits.
// denied_24h, when known, splits the bar into allowed and denied requests.
export type DomainHits = Record<
  string,
  { hits_24h: number; hits_7d: number; denied_24h?: number }
>;

// DomainEditor is the shared allowlist editor used by the Clients "Доступ" tab
// and the Policies screen. Adding parses raw input (URLs, "*.x", paste blobs),
// dedups with OR on include_subdomains; toggling/removing acts on one row. When
// readOnly (a shared policy with foreign clients for a manager), the list is
// shown with a note and no edit controls.
export function DomainEditor({
  destinations,
  hits,
  readOnly,
  readOnlyReason,
  onChange,
}: {
  destinations: Destination[];
  hits?: DomainHits;
  readOnly?: boolean;
  readOnlyReason?: string;
  onChange: (next: Destination[]) => void;
}) {
  const [draft, setDraft] = useState("");
  // Applies to plain names; "*.x" input always includes subdomains.
  const [withSubdomains, setWithSubdomains] = useState(true);
  const max = Math.max(
    1,
    ...destinations.map((d) => hits?.[d.domain]?.hits_24h ?? 0),
  );
  const add = () => {
    const tokens = draft
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const parsed = tokens
      .map((t) => parseDomainInput(t))
      .filter((d): d is NonNullable<typeof d> => d !== null)
      .map((d) => ({
        ...d,
        include_subdomains: d.include_subdomains || withSubdomains,
      }));
    if (parsed.length === 0) {
      setDraft("введите домен, например registry.npmjs.org");
      return;
    }
    onChange(mergeDestinations(destinations, parsed));
    setDraft("");
  };
  return (
    <div className="flex flex-col gap-2">
      {readOnly && readOnlyReason && (
        <Callout tone="info" icon={<Lock aria-hidden />} className="my-2">
          <div className="text-sm">{readOnlyReason}</div>
        </Callout>
      )}
      <Table className="table-fixed">
        <colgroup>
          <col />
          <col className="w-28" />
          <col className="w-[208px]" />
          <col className="w-[60px]" />
        </colgroup>
        <TableHeader>
          <TableRow>
            <TableHead>Домен</TableHead>
            <TableHead>Поддомены</TableHead>
            <TableHead className="text-right">Запросов, 24 ч</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {destinations.map((d) => {
            const h = hits?.[d.domain]?.hits_24h ?? 0;
            const denied = Math.min(h, hits?.[d.domain]?.denied_24h ?? 0);
            // Only flagged once hit counts have loaded.
            const unused =
              !!hits &&
              Object.keys(hits).length > 0 &&
              (hits[d.domain]?.hits_7d ?? 0) === 0;
            return (
              <TableRow key={d.domain}>
                <TableCell>
                  <span
                    title={d.domain}
                    className={
                      "block truncate" +
                      (unused ? " text-muted-foreground" : "")
                    }
                  >
                    {d.domain}
                  </span>
                  {unused && (
                    <Badge
                      variant="secondary"
                      className="mt-0.5"
                      title="Ни одного запроса за 7 дней"
                    >
                      не используется
                    </Badge>
                  )}
                </TableCell>
                <TableCell>
                  <Switch
                    checked={d.include_subdomains}
                    disabled={readOnly}
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
                <TableCell>
                  <div className="flex items-center justify-end gap-2">
                    <div
                      className={
                        "flex h-1.5 w-20 shrink-0 overflow-hidden rounded-full" +
                        (h ? " bg-muted" : "")
                      }
                      aria-hidden
                    >
                      <div
                        className="h-full bg-s-allow"
                        style={{ width: `${((h - denied) / max) * 100}%` }}
                      />
                      <div
                        className="h-full bg-s-deny"
                        style={{ width: `${(denied / max) * 100}%` }}
                      />
                    </div>
                    <span
                      className={
                        "w-[72px] text-right tabular-nums" +
                        (denied ? " text-bad" : "")
                      }
                    >
                      {!h ? "—" : denied ? `${h} · ${denied} отк.` : h}
                    </span>
                  </div>
                </TableCell>
                <TableCell className="text-right">
                  {!readOnly && (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Удалить ${d.domain}`}
                      onClick={() =>
                        onChange(
                          destinations.filter((x) => x.domain !== d.domain),
                        )
                      }
                    >
                      <X aria-hidden />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            );
          })}
          {destinations.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="text-sm text-muted-foreground">
                Нет доменов
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {!readOnly && (
        <div className="mt-1 flex items-center gap-2">
          <Input
            aria-label="Добавить домены"
            className="font-mono text-sm"
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
          <Label className="flex items-center gap-1.5 text-sm font-normal whitespace-nowrap">
            <Switch
              checked={withSubdomains}
              onCheckedChange={setWithSubdomains}
            />
            с поддоменами
          </Label>
          <Button variant="outline" onClick={add}>
            <Plus aria-hidden /> Добавить
          </Button>
        </div>
      )}
      {!readOnly && (
        <Disclosure
          className="text-sm text-muted-foreground"
          label="Как добавить список"
        >
          <p className="mt-1">
            Вставьте домены или ссылки через пробел, запятую или новую строку.
            Повторы объединяются.
          </p>
        </Disclosure>
      )}
    </div>
  );
}

// Undo placeholder kept for symmetry with the prototype's "вернуть" action on
// deleted rows; deletions here are immediate through onChange.
export function UndoRow({ onUndo }: { onUndo: () => void }) {
  return (
    <Button size="xs" variant="ghost" onClick={onUndo}>
      <Undo aria-hidden /> Вернуть
    </Button>
  );
}
