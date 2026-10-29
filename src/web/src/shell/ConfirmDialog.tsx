import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export type ConfirmOptions = {
  title: string;
  text: string;
  ok: string;
  danger?: boolean;
};

// Single confirmation dialog used by destructive actions (discard draft,
// delete client, block user). aria-describedby wired via DialogDescription.
export function ConfirmDialog({
  options,
  onConfirm,
  onCancel,
}: {
  options: ConfirmOptions | null;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <Dialog
      open={options !== null}
      onOpenChange={(open) => {
        if (!open) onCancel();
      }}
    >
      <DialogContent aria-describedby="confirm-desc" className="confirm-dialog">
        {options && (
          <>
            <DialogHeader>
              <DialogTitle>{options.title}</DialogTitle>
              <DialogDescription id="confirm-desc">
                {options.text}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="outline" onClick={onCancel}>
                Отмена
              </Button>
              <Button
                variant={options.danger ? "destructive" : "default"}
                onClick={onConfirm}
              >
                {options.ok}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
