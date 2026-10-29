import { useState } from "react";
import { Globe, Lock, Plus, Undo, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Destination } from "../model";
import { mergeDestinations, parseDomainInput } from "../policies/domains";

// Hit counts per domain (24h / 7d) from /inspector/policies/{id}/hits.
export type DomainHits = Record<
  string,
  { hits_24h: number; hits_7d: number }
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
  const max = Math.max(
    1,
    ...destinations.map((d) => hits?.[d.domain]?.hits_24h ?? 0),
  );
  const add = () => {
    const tokens = draft.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean);
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
    <div className="domain-editor">
      {!readOnly && (
        <div className="row" style={{ gap: 8, marginTop: 8 }}>
          <Input
            className="mono"
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
        <div className="callout callout-info" style={{ margin: "8px 0" }}>
          <Lock aria-hidden />
          <div className="small">{readOnlyReason}</div>
        </div>
      )}
      <div className="tw">
        <table style={{ width: "100%" }}>
          <thead>
            <tr>
              <th style={{ textAlign: "left" }}>Домен</th>
              <th>Поддомены</th>
              <th style={{ textAlign: "right" }}>Запросов, 24 ч</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {destinations.map((d) => {
              const h = hits?.[d.domain]?.hits_24h ?? 0;
              return (
                <tr key={d.domain}>
                  <td>
                    <span className="chip">
                      {d.include_subdomains ? "*." : ""}
                      {d.domain}
                    </span>
                  </td>
                  <td>
                    {readOnly ? (
                      d.include_subdomains ? "да" : "нет"
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
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <div className="row" style={{ gap: 8 }}>
                      <div className="bar grow">
                        <i style={{ width: `${(h / max) * 100}%` }} />
                      </div>
                      <span className="num small" style={{ width: 52 }}>
                        {h ? h : "—"}
                      </span>
                    </div>
                  </td>
                  <td style={{ textAlign: "right" }}>
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
                  </td>
                </tr>
              );
            })}
            {destinations.length === 0 && (
              <tr>
                <td colSpan={4} className="muted-text small">
                  Нет доменов
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {!readOnly && (
        <Label className="help muted-text small">
          Вставьте список из README или pip/npm-ошибки: домены извлекутся,
          дубли и пересечения подсветятся до сохранения.
        </Label>
      )}
      {readOnly && destinations.length === 0 && (
        <div className="muted-text small">
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
