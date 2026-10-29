import { useEffect } from "react";
import { Eye, Play, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import type { Draft, Operation, Preview, Status } from "../model";
import type { ApplyRun } from "./useApplyFlow";

// Side sheet: shows the diff between draft and active revision, then the
// revision -> validate -> apply steps with their live outcomes. The shell
// passes a pre-fetched preview (admin) or null (manager: preview is 403).
export function ReviewSheet({
  open,
  onOpenChange,
  draft,
  preview,
  status,
  run,
  onRefreshDraft,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  draft: Draft | null;
  preview: Preview | null;
  status: Status | null;
  run: ApplyRun;
  onRefreshDraft: () => void;
}) {
  useEffect(() => {
    // The reducer must be idle when the sheet opens so a previous run's
    // failure/conflict state doesn't bleed into a new review.
    if (open && run.state.phase !== "running") {
      // only reset when not mid-run
      if (
        run.state.phase === "failed" ||
        run.state.phase === "conflict" ||
        run.state.phase === "rolled_back" ||
        run.state.phase === "done"
      ) {
        run.reset();
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const { state, operation, error } = run;
  const running = state.phase === "running";
  const summary =
    preview?.summary.length || draft
      ? preview?.summary.length
        ? preview.summary
        : ["Черновик относительно текущей ревизии"]
      : ["Изменений нет"];
  const rev = preview?.revision_id ?? null;
  const blocked =
    status?.runtime.state === "degraded" || status?.squid.state === "down";

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        showCloseButton={false}
        className="review-sheet"
        aria-describedby="review-desc"
      >
        <SheetHeader>
          <SheetTitle>Проверка изменений</SheetTitle>
          <SheetDescription id="review-desc">
            {rev
              ? `Ревизия ${short(rev)} против действующей конфигурации`
              : "Черновик против действующей ревизии"}
          </SheetDescription>
        </SheetHeader>
        <div className="review-body">
          {blocked && (
            <div className="callout callout-bad">
              <strong>Squid недоступен или ограничен.</strong> Применить
              изменения нельзя, пока он не поднимется. Черновик сохранён.
            </div>
          )}
          {state.phase === "conflict" && (
            <div className="callout callout-warn">
              <strong>Черновик изменён с момента открытия проверки.</strong>{" "}
              Обновите черновик, чтобы применить актуальные изменения.
              <div className="callout-actions">
                <Button size="sm" variant="outline" onClick={onRefreshDraft}>
                  <RefreshCw aria-hidden /> Обновить черновик
                </Button>
              </div>
            </div>
          )}
          {state.phase === "failed" && state.code && (
            <FailureCallout code={state.code} operation={operation} />
          )}
          {state.phase === "rolled_back" && (
            <div className="callout callout-bad">
              <strong>
                Применение отклонено, Squid вернулся к прежней ревизии.
              </strong>{" "}
              Трафик не прерван. {operation?.findings.join(" ") || ""}
            </div>
          )}
          <section className="review-changes">
            <h3 className="review-section-title">Изменения</h3>
            <ul className="change-list">
              {summary.map((s, i) => (
                <li key={i} className="change-row">
                  <span className="change-mod">~</span>
                  <span>{s}</span>
                </li>
              ))}
            </ul>
          </section>
          <section className="review-steps">
            <h3 className="review-section-title">Ход применения</h3>
            <ol className="step-list">
              <StepRow
                label="Создание ревизии"
                done={isStepDone(state.step, "revision", running)}
                active={running && state.step === "revision"}
                failed={state.phase === "failed" && state.step === "revision"}
              />
              <StepRow
                label="Проверка конфигурации в Squid"
                done={isStepDone(state.step, "validate", running)}
                active={running && state.step === "validate"}
                failed={state.phase === "failed" && state.step === "validate"}
              />
              <StepRow
                label="Применение и подтверждение"
                done={state.phase === "done"}
                active={running && state.step === "apply"}
                failed={state.phase === "failed" && state.step === "apply"}
              />
            </ol>
          </section>
        </div>
        <SheetFooter>
          {running ? (
            <span className="review-foot-hint">
              Применение… не закрывайте окно
            </span>
          ) : state.phase === "done" ? (
            <>
              <span className="review-foot-hint">
                Ревизия применена и подтверждена Squid
              </span>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Закрыть
              </Button>
            </>
          ) : state.phase === "rolled_back" || state.phase === "failed" ? (
            <>
              <span className="review-foot-hint">
                Черновик сохранён без изменений
              </span>
              <Button variant="outline" onClick={run.reset}>
                <RefreshCw aria-hidden /> Вернуться к черновику
              </Button>
            </>
          ) : state.phase === "conflict" ? (
            <Button variant="outline" onClick={onRefreshDraft}>
              <RefreshCw aria-hidden /> Обновить черновик
            </Button>
          ) : (
            <>
              <span className="review-foot-hint">
                При ошибке Squid вернётся к прежней ревизии
              </span>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                <Eye aria-hidden /> Закрыть
              </Button>
              <Button disabled={blocked} onClick={run.run}>
                <Play aria-hidden /> Применить
              </Button>
            </>
          )}
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

function FailureCallout({
  code,
  operation,
}: {
  code: string;
  operation: Operation | null;
}) {
  const findings = operation?.findings.join(" ") || "";
  const isOutOfScope = code === "draft_out_of_scope";
  return (
    <div className="callout callout-bad">
      {isOutOfScope ? (
        <>
          <strong>В черновике есть изменения вне вашей области.</strong>{" "}
          Применить целиком может только администратор. Ваши изменения
          сохранены.
        </>
      ) : code === "rejected" ? (
        <>
          <strong>Squid отклонил конфигурацию.</strong> Текст ошибки не
          показывается, в нём могут быть секреты. Ничего не применено.
        </>
      ) : (
        <>
          <strong>Применение не удалось.</strong> {code}
          {findings ? ` · ${findings}` : ""} Ничего не применено, Squid работает
          на прежней ревизии.
        </>
      )}
    </div>
  );
}

function isStepDone(
  step: string | undefined,
  target: string,
  running: boolean,
): boolean {
  if (step === undefined) return false;
  const order = ["revision", "validate", "apply"];
  const cur = order.indexOf(step);
  const tgt = order.indexOf(target);
  if (cur === -1 || tgt === -1) return false;
  // A step is "done" if we have moved past it while running, or any step is
  // done once the flow reached the terminal "done" phase (handled by callers
  // via the explicit done flag). During running, lower steps are done.
  return running ? cur > tgt : false;
}

function StepRow({
  label,
  done,
  active,
  failed,
}: {
  label: string;
  done: boolean;
  active: boolean;
  failed: boolean;
}) {
  return (
    <li className={"step-row" + (active ? " active" : "")}>
      <span
        className={
          "step-marker " +
          (failed ? "failed" : done ? "done" : active ? "active" : "")
        }
        aria-hidden
      >
        {failed ? (
          <X />
        ) : done ? (
          "✓"
        ) : active ? (
          <span className="spinner" />
        ) : (
          "·"
        )}
      </span>
      <span className={active || done ? "" : "muted-text"}>{label}</span>
    </li>
  );
}

function short(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}
