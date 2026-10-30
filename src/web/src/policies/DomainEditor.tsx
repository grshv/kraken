import { useState } from "react";
import { Globe, Lock, Plus, Undo, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Callout, Chip } from "@/components/kraken/blocks";
import type { Destination } from "../model";
import { mergeDestinations, parseDomainInput } from "../policies/domains";

// Hit counts per domain (24h / 7d) from /inspector/policies/{id}/hits.
export type DomainHits = Record<string, { hits_24h: number; hits_7d: number }>;

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
      .filter((d): d is NonNullable<typeof d> => d !== null);
    if (parsed.length === 0) {
      setDraft("введите домен, например registry.npmjs.org");
      return;
    }
    onChange(mergeDestinations(destinations, parsed));
    setDraft("");
  };
  return (
    <div className="flex flex-col gap-2">
      {!readOnly && (
        <div className="mt-2 flex items-center gap-2">
          <Input
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
          <Button size="sm" onClick={add}>
            <Plus aria-hidden /> Добавить
          </Button>
        </div>
      )}
      {readOnly && readOnlyReason && (
        <Callout tone="info" icon={<Lock aria-hidden />} className="my-2">
          <div className="text-sm">{readOnlyReason}</div>
        </Callout>
      )}
      <Table>
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
            return (
              <TableRow key={d.domain}>
                <TableCell>
                  <Chip>
                    {d.include_subdomains ? "*." : ""}
                    {d.domain}
                  </Chip>
                </TableCell>
                <TableCell>
                  {readOnly ? (
                    d.include_subdomains ? (
                      "да"
                    ) : (
                      "нет"
                    )
                  ) : (
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-pressed={d.include_subdomains}
                      aria-label={`Поддомены ${d.domain}`}
                      onClick={() =>
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
                    >
                      {d.include_subdomains ? "да" : "нет"}
                    </Button>
                  )}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <div className="h-1.5 min-w-[50px] flex-1 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-s-allow"
                        style={{ width: `${(h / max) * 100}%` }}
                      />
                    </div>
                    <span className="w-[52px] text-right tabular-nums text-sm">
                      {h ? h : "—"}
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
        <div className="text-sm text-muted-foreground">
          Вставьте список из README или pip/npm-ошибки: домены извлекутся, дубли
          и пересечения подсветятся до сохранения.
        </div>
      )}
      {readOnly && destinations.length === 0 && (
        <div className="text-sm text-muted-foreground">
          <Globe aria-hidden /> Разрешены любые внешние сайты.
        </div>
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
