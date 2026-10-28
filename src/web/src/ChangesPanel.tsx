import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { StatusBadge } from "./StatusBadge";
import type { Preview, Operation, RevisionSummary } from "./model";
type Props = {
  preview: Preview | null;
  operation: Operation | null;
  history: RevisionSummary[];
  busy: boolean;
  validated: boolean;
  rollback: boolean;
  canApply: boolean;
  blocked: boolean;
  onRefresh: () => void;
  onValidate: () => void;
  onApply: () => void;
  onHistory: (rev: RevisionSummary) => void;
};
export function ChangesPanel({
  preview,
  operation,
  history,
  busy,
  validated,
  rollback,
  canApply,
  blocked,
  onRefresh,
  onValidate,
  onApply,
  onHistory,
}: Props) {
  const changed = preview && (preview.user_changes || preview.runtime_changes);
  return (
    <>
      <section className="section">
        <div className="section-head">
          <h2>
            {rollback
              ? "Откат конфигурации"
              : "Черновик и действующая конфигурация"}
          </h2>
          <Button variant="ghost" disabled={busy} onClick={onRefresh}>
            Обновить изменения
          </Button>
        </div>
        {!preview ? (
          <p className="empty">Загрузка сравнения…</p>
        ) : (
          <>
            {!changed ? (
              <p className="empty">Нет неприменённых изменений</p>
            ) : (
              <>
                {preview.user_changes && (
                  <ul className="change-summary">
                    {preview.summary.map((s, i) => (
                      <li key={i}>{s}</li>
                    ))}
                  </ul>
                )}
                {preview.runtime_changes && !preview.user_changes && (
                  <p className="runtime-update">
                    Настройки не менялись. Доступно обновление служебной
                    конфигурации Squid.
                  </p>
                )}
                <details className="technical-diff">
                  <summary>Изменения squid.conf</summary>
                  <pre
                    className="config-diff"
                    aria-label="Изменения squid.conf"
                  >
                    {preview.config_diff.map((line, i) => (
                      <span key={i} className={line.kind}>
                        {line.kind === "added"
                          ? "+ "
                          : line.kind === "removed"
                            ? "- "
                            : "  "}
                        {line.text}
                        {"\n"}
                      </span>
                    ))}
                  </pre>
                </details>
              </>
            )}
            <details>
              <summary>Итоговый squid.conf</summary>
              <pre>{preview.generated_config}</pre>
            </details>
            {changed && (
              <div className="section-footer">
                <span>
                  {rollback
                    ? "Конфигурация будет проверена перед откатом"
                    : validated
                      ? "Эта ревизия прошла проверку"
                      : "Требуется проверка конфигурации"}
                </span>
                <div className="actions">
                  <Button
                    variant="outline"
                    disabled={busy || rollback || blocked}
                    onClick={onValidate}
                  >
                    Проверить
                  </Button>
                  <Button
                    disabled={
                      busy || !canApply || (!rollback && !validated) || blocked
                    }
                    onClick={onApply}
                  >
                    {rollback ? "Откатить к этой ревизии" : "Применить"}
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
        {operation && (
          <div className="operation">
            <StatusBadge value={operation.status} />
            {operation.findings.length > 0 && (
              <ul className="findings">
                {operation.findings.map((finding, i) => (
                  <li key={i}>{finding}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </section>
      <section className="section">
        <div className="section-head">
          <h2>История ревизий</h2>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Создана</TableHead>
              <TableHead>Ревизия</TableHead>
              <TableHead>Статус</TableHead>
              <TableHead>
                <span className="sr-only">Действия</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.map((rev) => (
              <TableRow key={rev.id}>
                <TableCell>
                  {new Date(rev.created_at).toLocaleString("ru-RU")}
                </TableCell>
                <TableCell className="mono">{rev.id.slice(0, 8)}</TableCell>
                <TableCell>
                  <StatusBadge
                    value={rev.is_current ? "succeeded" : rev.latest_status}
                  />
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() => onHistory(rev)}
                  >
                    {rev.can_rollback
                      ? "Сравнить и откатить"
                      : "Посмотреть diff"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {!history.length && <p className="empty">Ревизий пока нет.</p>}
      </section>
    </>
  );
}
