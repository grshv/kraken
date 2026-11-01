import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { useEffect, useState } from "react";
import {
  Check,
  ChevronDown,
  Info,
  Play,
  RefreshCw,
  TriangleAlert,
  X,
} from "lucide-react";
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
import { cn } from "cn";
import type { Draft, Operation, Preview, Snapshot, Status } from "../model";
import { pluralRu, revisionLabel } from "../model";
import type { Request } from "../api";
import type { CommitResult } from "../sections/sectionProps";
import { ChangeList } from "./ChangeList";
import { canRevert, revertChange } from "./revert";
import type { ApplyRun } from "./useApplyFlow";

export type DraftImpact = {
  window_hours: number;
  newly_allowed: number;
  newly_denied: number;
  clients: { client_id: string; newly_allowed: number; newly_denied: number }[];
};

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
  active,
  onCommit,
  onOpenAudit,
  applyBlockedReason,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  draft: Draft | null;
  preview: Preview | null;
  status: Status | null;
  run: ApplyRun;
  request?: Request;
  onRefreshDraft: () => void;
  active?: Snapshot | null;
  onCommit?: (next: Snapshot) => Promise<CommitResult>;
  // Opens the audit entry of the revision just applied.
  onOpenAudit?: () => void;
  applyBlockedReason?: string;
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
  const activeLabel = revisionLabel(
    status?.applied_revision ?? "",
    status?.applied_revision_number,
  );
  // When the preview carries structured changes and the shell wired undo,
  // render the change list from changes with per-row "Отменить" buttons.
  // Otherwise fall back to the summary texts (audit/manager/running cases).
  const useChanges =
    !running && !!draft && !!active && !!onCommit && !!pv?.changes?.length;
  const changeItems = useChanges ? pv!.changes!.map((c) => c.text) : summary;
  const undoable = useChanges
    ? pv!.changes!.map((c) => canRevert(c))
    : undefined;
  const onUndo = useChanges
    ? (i: number) => {
        const change = pv!.changes![i];
        void onCommit!(revertChange(draft!.snapshot, active!, change));
      }
    : undefined;
  // The draft empties once applied; keep what was sent so the result still
  // shows what changed.
  const [applied, setApplied] = useState<string[]>([]);
  const itemsKey = changeItems.join("\n");
  useEffect(() => {
    if (state.phase === "idle") setApplied(changeItems);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.phase, itemsKey]);
  const finished = state.phase === "done";
  const rejected = state.phase === "rolled_back" || state.phase === "failed";
  const shownItems = finished && applied.length ? applied : changeItems;
  const appliedLabel = revisionLabel(
    status?.applied_revision ?? "",
    status?.applied_revision_number,
  );
  const hunks = diffHunks(pv?.config_diff ?? []);
  const clientName = (id: string) =>
    draft?.snapshot.clients.find((c) => c.id === id)?.name ?? id;
  const showSteps = state.phase !== "idle";
  const blocked =
    !!applyBlockedReason ||
    status?.runtime.state === "degraded" ||
    status?.squid.state === "down";

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
              {finished
                ? `Работает ${appliedLabel}`
                : status?.applied_revision
                  ? `Черновик против работающей ревизии ${activeLabel}`
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
          {!finished && applyBlockedReason && (
            <Callout tone="warn">{applyBlockedReason}</Callout>
          )}
          {!applyBlockedReason && blocked && (
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
                Применение отклонено на шаге «Применение и подтверждение».
              </strong>{" "}
              Squid не подтвердил новую ревизию: наблюдаемая конфигурация не
              совпала с отправленной. Kraken вернул {activeLabel}, трафик не
              прерван. {operation?.findings.join(" ") || ""} Изменённые
              фрагменты — ниже.
            </Callout>
          )}
          {finished && (
            <div className="flex items-baseline justify-between gap-2">
              <h3 className="font-semibold">Применено в {appliedLabel}</h3>
              {onOpenAudit && (
                <Button size="xs" variant="ghost" onClick={onOpenAudit}>
                  Открыть в аудите
                </Button>
              )}
            </div>
          )}
          {(finished || !applyBlockedReason || !!pv?.changes?.length) && (
            <Card variant="panel" className="gap-0 py-0">
              <CardContent className="py-0.5">
                <ChangeList
                  items={shownItems}
                  onUndo={finished ? undefined : onUndo}
                  undoable={finished ? undefined : undoable}
                  details={
                    useChanges && !finished
                      ? pv!.changes!.map((c) => c.details)
                      : undefined
                  }
                />
              </CardContent>
            </Card>
          )}
          <section className="flex flex-col gap-2">
            <h3 className="font-semibold">Влияние на трафик</h3>
            {impact &&
              impact.newly_allowed === 0 &&
              impact.newly_denied === 0 && (
                <p className="text-sm text-muted-foreground">
                  По запросам за последние {impact.window_hours} ч изменений
                  доступа не найдено.
                </p>
              )}
            {impact?.clients
              .filter((c) => c.newly_denied > 0)
              .map((c) => (
                <Callout
                  key={c.client_id}
                  tone="warn"
                  icon={<TriangleAlert aria-hidden />}
                >
                  <strong>{clientName(c.client_id)}:</strong>{" "}
                  {pluralRu(c.newly_denied, "запрос", "запроса", "запросов")} за{" "}
                  {impact.window_hours} ч были разрешены, они начнут получать
                  отказ.
                </Callout>
              ))}
            {impact && impact.newly_allowed > 0 && (
              <Callout tone="info" icon={<Check aria-hidden />}>
                Отказы, которые исчезнут:{" "}
                {pluralRu(
                  impact.newly_allowed,
                  "запрос",
                  "запроса",
                  "запросов",
                )}{" "}
                за {impact.window_hours} ч
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
            <Collapsible
              key={rejected ? "open" : "closed"}
              defaultOpen={rejected}
              className="group/diff"
            >
              <CollapsibleTrigger asChild>
                <Button
                  variant="ghost"
                  className="h-auto justify-start px-0 py-1 font-semibold"
                >
                  <ChevronDown className="transition-transform group-data-[state=open]/diff:rotate-180" />
                  Изменённые фрагменты squid.conf
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent>
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
              </CollapsibleContent>
            </Collapsible>
          )}
          {showSteps && (
            <Card variant="panel" className="gap-0 py-0">
              <CardContent className="py-2.5">
                <ol className="m-0 flex list-none flex-col gap-2 p-0">
                  <StepRow
                    label="Создание ревизии"
                    done={isStepDone(state.step, "revision", state.phase)}
                    active={running && state.step === "revision"}
                    failed={
                      state.phase === "failed" && state.step === "revision"
                    }
                  />
                  <StepRow
                    label="Проверка конфигурации в Squid"
                    done={isStepDone(state.step, "validate", state.phase)}
                    active={running && state.step === "validate"}
                    failed={
                      state.phase === "failed" && state.step === "validate"
                    }
                  />
                  <StepRow
                    label="Применение и подтверждение"
                    done={state.phase === "done"}
                    active={running && state.step === "apply"}
                    failed={rejected && state.step === "apply"}
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
                {appliedLabel} применена и подтверждена Squid
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
                {status?.applied_revision ? activeLabel : "прежней ревизии"}
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
  phase: string,
): boolean {
  if (phase === "done") return true;
  if (step === undefined) return false;
  const order = ["revision", "validate", "apply"];
  const cur = order.indexOf(step);
  const tgt = order.indexOf(target);
  if (cur === -1 || tgt === -1) return false;
  // Steps before the current one are done while running and after a failure
  // or rollback on a later step.
  return cur > tgt;
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
