import { useEffect, useState } from "react";
import { Check, Info, Play, RefreshCw, TriangleAlert, X } from "lucide-react";
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
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn } from "cn";
import type { Draft, Operation, Preview, Status } from "../model";
import { revisionLabel } from "../model";
import type { Request } from "../api";
import type { ApplyRun } from "./useApplyFlow";

export type DraftImpact = {
  window_hours: number;
  newly_allowed: number;
  newly_denied: number;
  clients: { client_id: string; newly_allowed: number; newly_denied: number }[];
};

// changeKind picks the change marker from a summary line of semanticDiff
// ("Добавлен клиент: x", "Удалён клиент: x"; anything else is a modification).
export function changeKind(line: string): "add" | "del" | "mod" {
  if (/(^|\s)добавлен[аоы]?(\s|$|:)/iu.test(line)) return "add";
  if (/(^|\s)удал[её]н[аоы]?(\s|$|:)/iu.test(line)) return "del";
  return "mod";
}

// diffHunks keeps only changed squid.conf lines with `context` unchanged lines
// around them; a gap between hunks becomes null (rendered as "…").
export function diffHunks(
  lines: Preview["config_diff"],
  context = 2,
): (Preview["config_diff"][number] | null)[] {
  const keep = new Set<number>();
  lines.forEach((l, i) => {
    if (l.kind === "context") return;
    for (let j = i - context; j <= i + context; j++) keep.add(j);
  });
  const out: (Preview["config_diff"][number] | null)[] = [];
  let last = -1;
  lines.forEach((l, i) => {
    if (!keep.has(i)) return;
    if (last >= 0 && i > last + 1) out.push(null);
    out.push(l);
    last = i;
  });
  return out;
}

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
  request,
  onRefreshDraft,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  draft: Draft | null;
  preview: Preview | null;
  status: Status | null;
  run: ApplyRun;
  request?: Request;
  onRefreshDraft: () => void;
}) {
  // The sheet loads a fresh preview (admin; a manager gets 403 and keeps the
  // prop) and the 24 h traffic impact each time it opens.
  const [fresh, setFresh] = useState<Preview | null>(null);
  const [impact, setImpact] = useState<DraftImpact | null>(null);
  useEffect(() => {
    if (!open || !request) return;
    let live = true;
    setFresh(null);
    setImpact(null);
    request<Preview>("/configuration/preview")
      .then((p) => live && setFresh(p))
      .catch(() => {});
    request<DraftImpact>("/inspector/draft-impact")
      .then((v) => live && setImpact(v))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [open, request, draft?.version]);

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
  const pv = fresh ?? preview;
  const summary =
    pv?.summary.length || draft
      ? pv?.summary.length
        ? pv.summary
        : ["Черновик относительно текущей ревизии"]
      : ["Изменений нет"];
  const active = revisionLabel(
    status?.applied_revision ?? "",
    status?.applied_revision_number,
  );
  const hunks = diffHunks(pv?.config_diff ?? []);
  const clientName = (id: string) =>
    draft?.snapshot.clients.find((c) => c.id === id)?.name ?? id;
  const showSteps = state.phase !== "idle";
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
        <SheetHeader className="flex-row items-start gap-3 border-b px-5 py-4">
          <div className="min-w-0 grow">
            <SheetTitle className="text-xl font-semibold">
              Проверка изменений
            </SheetTitle>
            <SheetDescription id="review-desc">
              {status?.applied_revision
                ? `Черновик против работающей ревизии ${active}`
                : "Черновик против пустой конфигурации"}
            </SheetDescription>
          </div>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label="Закрыть"
            onClick={() => onOpenChange(false)}
          >
            <X aria-hidden />
          </Button>
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
            <CardContent className="py-0.5">
              <ul className="m-0 flex list-none flex-col p-0">
                {summary.map((s, i) => (
                  <li
                    key={i}
                    className="grid grid-cols-[22px_minmax(0,1fr)] items-start gap-2.5 border-b py-2.5 last:border-b-0"
                  >
                    <Badge
                      variant={changeKind(s)}
                      className="grid size-5 place-items-center px-0 font-mono font-semibold"
                    >
                      {{ add: "+", del: "−", mod: "~" }[changeKind(s)]}
                    </Badge>
                    <span>{s}</span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
          <section className="flex flex-col gap-2">
            <h3 className="font-semibold">Влияние на трафик</h3>
            {impact?.clients
              .filter((c) => c.newly_denied > 0)
              .map((c) => (
                <Callout
                  key={c.client_id}
                  tone="warn"
                  icon={<TriangleAlert aria-hidden />}
                >
                  <strong>{clientName(c.client_id)}:</strong> {c.newly_denied}{" "}
                  запросов за {impact.window_hours} ч были разрешены, они начнут
                  получать отказ.
                </Callout>
              ))}
            {impact && impact.newly_allowed > 0 && (
              <Callout tone="info" icon={<Check aria-hidden />}>
                Отказы, которые исчезнут: {impact.newly_allowed} запросов за{" "}
                {impact.window_hours} ч
                {impact.clients.some((c) => c.newly_allowed > 0)
                  ? ` (${impact.clients
                      .filter((c) => c.newly_allowed > 0)
                      .map((c) => clientName(c.client_id))
                      .join(", ")})`
                  : ""}
                .
              </Callout>
            )}
            <Callout tone="info" icon={<Info aria-hidden />}>
              Открытые туннели не разрываются; новые правила действуют для новых
              соединений.
            </Callout>
          </section>
          {hunks.length > 0 && (
            <details>
              <summary className="cursor-pointer font-semibold">
                Изменённые фрагменты squid.conf
              </summary>
              <pre className="mt-2 overflow-x-auto rounded-lg border bg-card p-2.5 font-mono text-sm leading-relaxed">
                {hunks.map((l, i) =>
                  l === null ? (
                    <div key={i} className="text-muted-foreground">
                      …
                    </div>
                  ) : (
                    <div
                      key={i}
                      className={cn(
                        "whitespace-pre",
                        l.kind === "added" && "bg-add-bg text-add-fg",
                        l.kind === "removed" && "bg-del-bg text-del-fg",
                      )}
                    >
                      {l.kind === "added"
                        ? "+ "
                        : l.kind === "removed"
                          ? "− "
                          : "  "}
                      {l.text}
                    </div>
                  ),
                )}
              </pre>
            </details>
          )}
          {showSteps && (
            <Card className="gap-0 py-0">
              <CardContent className="py-2.5">
                <ol className="m-0 flex list-none flex-col gap-2 p-0">
                  <StepRow
                    label="Создание ревизии"
                    done={isStepDone(state.step, "revision", running)}
                    active={running && state.step === "revision"}
                    failed={
                      state.phase === "failed" && state.step === "revision"
                    }
                  />
                  <StepRow
                    label="Проверка конфигурации в Squid"
                    done={isStepDone(state.step, "validate", running)}
                    active={running && state.step === "validate"}
                    failed={
                      state.phase === "failed" && state.step === "validate"
                    }
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
          )}
        </div>
        <SheetFooter className="flex-row items-center gap-2 border-t px-5 py-3">
          {running ? (
            <span className="text-sm text-muted-foreground">
              Применение… не закрывайте окно
            </span>
          ) : state.phase === "done" ? (
            <>
              <span className="grow text-sm text-muted-foreground">
                Ревизия применена и подтверждена Squid
              </span>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Закрыть
              </Button>
            </>
          ) : state.phase === "rolled_back" || state.phase === "failed" ? (
            <>
              <span className="grow text-sm text-muted-foreground">
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
              <span className="grow text-sm text-muted-foreground">
                При ошибке Squid автоматически вернётся к{" "}
                {status?.applied_revision ? active : "прежней ревизии"}
              </span>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Закрыть
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
