import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/kraken/blocks";

// passwordProblem checks the new password client-side with the same rules as
// the server (12..1024 bytes, differs from the current one, typed twice).
export function passwordProblem(
  current: string,
  next: string,
  repeat: string,
): string {
  const bytes = new TextEncoder().encode(next).length;
  if (bytes < 12) return "Не короче 12 символов";
  if (bytes > 1024) return "Слишком длинный пароль";
  if (next === current) return "Совпадает с текущим";
  if (repeat && repeat !== next) return "Пароли не совпадают";
  return "";
}

// PasswordDialog changes the signed-in user's own password. The current
// session stays; other sessions of the user end on the server.
export function PasswordDialog({
  open,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (current: string, next: string) => Promise<void>;
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const problem = next ? passwordProblem(current, next, repeat) : "";
  const ready = current && next && repeat === next && !problem;
  const close = (v: boolean) => {
    if (!v) {
      setCurrent("");
      setNext("");
      setRepeat("");
      setError("");
    }
    onOpenChange(v);
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle>Сменить пароль</DialogTitle>
          <DialogDescription>
            Эта сессия останется, остальные ваши сессии завершатся.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!ready || busy) return;
            setBusy(true);
            setError("");
            try {
              await onSubmit(current, next);
              close(false);
            } catch (err) {
              setError((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field label="Текущий пароль" htmlFor="f-pw-current" error={error}>
            <Input
              id="f-pw-current"
              type="password"
              autoComplete="current-password"
              autoFocus
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </Field>
          <Field
            label="Новый пароль"
            htmlFor="f-pw-new"
            error={problem && problem !== "Пароли не совпадают" ? problem : ""}
            help="Не короче 12 символов"
          >
            <Input
              id="f-pw-new"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </Field>
          <Field
            label="Повторите новый пароль"
            htmlFor="f-pw-repeat"
            error={problem === "Пароли не совпадают" ? problem : ""}
          >
            <Input
              id="f-pw-repeat"
              type="password"
              autoComplete="new-password"
              value={repeat}
              onChange={(e) => setRepeat(e.target.value)}
            />
          </Field>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => close(false)}
            >
              Отмена
            </Button>
            <Button type="submit" disabled={!ready || busy}>
              Сменить пароль
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
