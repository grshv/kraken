import { Alert } from "@/components/ui/alert";
import { Disclosure } from "./components/Disclosure";
import { Panel } from "./components/Panel";
import { useEffect, useState } from "react";
import { EntityDialog } from "./components/EntityDialog";
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
  reviewedRevision: RevisionSummary | null;
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
  reviewedRevision,
  canApply,
  blocked,
  onRefresh,
  onValidate,
  onApply,
  onHistory,
}: Props) {
  const changed =
    !!preview && (preview.user_changes || preview.runtime_changes);
  const [confirm, setConfirm] = useState(false);
  useEffect(() => setConfirm(false), [preview, reviewedRevision]);
  const readonly = !!reviewedRevision && !rollback;
  const added =
    preview?.config_diff.filter((line) => line.kind === "added").length ?? 0;
  const removed =
    preview?.config_diff.filter((line) => line.kind === "removed").length ?? 0;
  const pending =
    busy &&
    operation &&
    ["pending", "validating", "reloading"].includes(operation.status);
  return (
    <>
      <Panel className="section changes-review" aria-busy={busy}>
        <div className="section-head">
          <div>
            <h2>
              {reviewedRevision
                ? `Ревизия ${reviewedRevision.id.slice(0, 8)}`
                : "Неприменённые изменения"}
            </h2>
            <p className="review-source">
              Действующая конфигурация →{" "}
              {reviewedRevision
                ? `ревизия от ${new Date(reviewedRevision.created_at).toLocaleString("ru-RU")}`
                : "сохранённый черновик"}
            </p>
          </div>
          <Button variant="outline" disabled={busy} onClick={onRefresh}>
            {reviewedRevision ? "К черновику" : "Обновить сравнение"}
          </Button>
        </div>
        {!preview ? (
          <p className="empty">Загрузка сравнения…</p>
        ) : !changed ? (
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
                Доступно обновление служебной конфигурации Squid.
              </p>
            )}
            <div className="diff-toolbar">
              <span>squid.conf</span>
              <span aria-label="Количество изменённых строк">
                <span className="diff-added">+{added}</span> /{" "}
                <span className="diff-removed">−{removed}</span>
              </span>
            </div>
            <pre className="config-diff" aria-label="Изменения squid.conf">
              {preview.config_diff.map((line, i) => (
                <span
                  key={i}
                  className={line.text.startsWith("@@") ? "hunk" : line.kind}
                >
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
            {!readonly && (
              <div className="section-footer review-actions">
                <span role="status">
                  {pending
                    ? operation.action === "validate"
                      ? "Проверка Squid…"
                      : "Применение конфигурации…"
                    : rollback
                      ? "Откат будет проверен перед применением"
                      : validated
                        ? "Проверка Squid пройдена. Можно применить."
                        : "Сначала проверьте конфигурацию Squid"}
                </span>
                <div className="actions">
                  {!rollback && (
                    <Button
                      variant={validated ? "outline" : "default"}
                      disabled={busy || blocked}
                      onClick={onValidate}
                    >
                      {validated ? "Проверить повторно" : "Проверить"}
                    </Button>
                  )}
                  {(validated || rollback) && (
                    <Button
                      disabled={busy || !canApply || blocked}
                      onClick={() => setConfirm(true)}
                    >
                      {rollback ? "Откатить к этой ревизии" : "Применить"}
                    </Button>
                  )}
                </div>
              </div>
            )}
            {blocked && (
              <Alert variant="destructive" className="error">
                Применение заблокировано: требуется восстановление рабочего
                состояния Squid.
              </Alert>
            )}
          </>
        )}
        {preview && (
          <Disclosure
            className="full-config"
            title={<> Полный итоговый squid.conf </>}
          >
            <pre>{preview.generated_config}</pre>
          </Disclosure>
        )}
        {operation && operation.status !== "validated" && (
          <div className="operation" role="status">
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
      </Panel>
      <EntityDialog
        open={confirm}
        onOpenChange={setConfirm}
        busy={busy}
        title={rollback ? "Подтвердить откат" : "Применить конфигурацию?"}
        submitLabel={rollback ? "Подтвердить откат" : "Подтвердить применение"}
        submitDisabled={
          !changed || blocked || !canApply || (!rollback && !validated)
        }
        onSubmit={(event) => {
          event.preventDefault();
          setConfirm(false);
          onApply();
        }}
      >
        <p>Новые соединения клиентов будут обрабатываться по этим правилам.</p>
        <ul className="review-confirm-summary">
          {preview?.summary.map((s, i) => (
            <li key={i}>{s}</li>
          ))}
        </ul>
        <p className="muted">
          Уже открытые CONNECT-туннели могут работать до закрытия соединения.
        </p>
      </EntityDialog>
      <Panel className="section">
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
      </Panel>
    </>
  );
}
