import { useRef, type ReactNode, type SubmitEventHandler } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  busy: boolean;
  title: string;
  onSubmit: SubmitEventHandler<HTMLFormElement>;
  children: ReactNode;
  error?: string;
  submitLabel?: string;
  submitDisabled?: boolean;
  busyLabel?: string;
  formLabel?: string;
};

// Client and account editors share one modal layout, dismiss policy and footer.
// Radix supplies focus trapping, Escape handling and background scroll locking.
export function EntityDialog({
  open,
  onOpenChange,
  busy,
  title,
  onSubmit,
  children,
  error,
  submitLabel = "Сохранить",
  submitDisabled = false,
  busyLabel = "Сохранение…",
  formLabel,
}: Props) {
  const previousFocus = useRef<HTMLElement | null>(null);
  const previouslyOpen = useRef(false);
  // Snapshot the opener before the portal mounts. React autoFocus in a child
  // runs before Radix onOpenAutoFocus and would otherwise capture that input.
  if (open && !previouslyOpen.current) {
    previousFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
  }
  previouslyOpen.current = open;
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next);
      }}
    >
      <DialogContent
        className="entity-dialog max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-[580px]"
        showCloseButton={false}
        aria-describedby={undefined}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          if (previousFocus.current?.isConnected) previousFocus.current.focus();
        }}
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (busy) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(event) => {
            if (busy || submitDisabled) {
              event.preventDefault();
              return;
            }
            onSubmit(event);
          }}
          aria-label={formLabel}
          aria-busy={busy}
          className="grid gap-5"
        >
          <fieldset disabled={busy} className="grid min-w-0 gap-5">
            {children}
          </fieldset>
          {error && (
            <p className="error mb-0" role="alert">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => onOpenChange(false)}
            >
              Отмена
            </Button>
            <Button type="submit" disabled={busy || submitDisabled}>
              {busy ? busyLabel : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
