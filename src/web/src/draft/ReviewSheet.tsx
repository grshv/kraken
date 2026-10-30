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
import { Callout } from "@/components/kraken/blocks";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "cn";
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
        className="w-full gap-0 sm:w-[min(620px,100%)] sm:max-w-[620px]"
        aria-describedby="review-desc"
      >
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Проверка изменений</SheetTitle>
          <SheetDescription id="review-desc">
            {rev
              ? `Ревизия ${short(rev)} против действующей конфигурации`
              : "Черновик против действующей ревизии"}
          </SheetDescription>
        </SheetHeader>
        <div className="flex flex-1 flex-col gap-3.5 overflow-y-auto px-5 py-4">
          {blocked && (
            <Callout tone="bad">
              <strong>Squid недоступен или ограничен.</strong> Применить
              изменения нельзя, пока он не поднимется. Черновик сохранён.
            </Callout>
          )}
          {state.phase === "conflict" && (
            <Callout
              tone="warn"
              actions={
                <Button size="sm" variant="outline" onClick={onRefreshDraft}>
                  <RefreshCw aria-hidden /> Обновить черновик
                </Button>
              }
            >
              <strong>Черновик изменён с момента открытия проверки.</strong>{" "}
              Обновите черновик, чтобы применить актуальные изменения.
            </Callout>
          )}
          {state.phase === "failed" && state.code && (
            <FailureCallout code={state.code} operation={operation} />
          )}
          {state.phase === "rolled_back" && (
            <Callout tone="bad">
              <strong>
                Применение отклонено, Squid вернулся к прежней ревизии.
              </strong>{" "}
              Трафик не прерван. {operation?.findings.join(" ") || ""}
            </Callout>
          )}
          <Card className="gap-0 py-0">
            <CardHeader className="border-b px-3.5 py-2.5">
              <h3 className="text-lg font-semibold">Изменения</h3>
            </CardHeader>
            <CardContent className="py-0.5">
              <ul className="m-0 flex list-none flex-col p-0">
                {summary.map((s, i) => (
                  <li
                    key={i}
                    className="grid grid-cols-[22px_minmax(0,1fr)] items-start gap-2.5 border-b py-2.5 last:border-b-0"
                  >
                    <Badge
                      variant="mod"
                      className="grid size-5 place-items-center px-0 font-mono font-semibold"
                    >
                      ~
                    </Badge>
                    <span>{s}</span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
          <Card className="gap-0 py-0">
            <CardHeader className="border-b px-3.5 py-2.5">
              <h3 className="text-lg font-semibold">Ход применения</h3>
            </CardHeader>
            <CardContent className="py-2.5">
              <ol className="m-0 flex list-none flex-col gap-2 p-0">
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
            </CardContent>
          </Card>
        </div>
        <SheetFooter className="flex-row items-center gap-2 border-t px-5 py-3">
          {running ? (
            <span className="text-sm text-muted-foreground">
              Применение… не закрывайте окно
            </span>
          ) : state.phase === "done" ? (
            <>
              <span className="text-sm text-muted-foreground">
                Ревизия применена и подтверждена Squid
              </span>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Закрыть
              </Button>
            </>
          ) : state.phase === "rolled_back" || state.phase === "failed" ? (
            <>
              <span className="text-sm text-muted-foreground">
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
              <span className="text-sm text-muted-foreground">
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
    <Callout tone="bad">
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
    </Callout>
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
    <li className="grid grid-cols-[20px_minmax(0,1fr)] items-center gap-2 text-base">
      <span
        className={cn(
          "grid size-[18px] place-items-center text-sm [&_svg]:size-3.5",
          failed
            ? "text-bad"
            : done
              ? "text-ok"
              : active
                ? "text-foreground"
                : "",
        )}
        aria-hidden
      >
        {failed ? (
          <X />
        ) : done ? (
          "✓"
        ) : active ? (
          <span className="size-3.5 animate-spin rounded-full border-2 border-border border-t-foreground" />
        ) : (
          "·"
        )}
      </span>
      <span className={active || done ? "" : "text-muted-foreground"}>
        {label}
      </span>
    </li>
  );
}

function short(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}
