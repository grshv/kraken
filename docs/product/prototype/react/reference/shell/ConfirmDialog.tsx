import { buttonVariants } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

export type ConfirmOptions = {
  title: string;
  text: string;
  ok: string;
  danger?: boolean;
};

// Single confirmation dialog used by destructive actions (discard draft,
// delete client, block user). aria-describedby wired via AlertDialogDescription.
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
    <AlertDialog
      open={options !== null}
      onOpenChange={(open) => {
        if (!open) onCancel();
      }}
    >
      <AlertDialogContent
        aria-describedby="confirm-desc"
        className="sm:max-w-[440px]"
      >
        {options && (
          <>
            <AlertDialogHeader>
              <AlertDialogTitle>{options.title}</AlertDialogTitle>
              <AlertDialogDescription id="confirm-desc">
                {options.text}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel onClick={onCancel}>Отмена</AlertDialogCancel>
              <AlertDialogAction
                className={
                  options.danger
                    ? buttonVariants({ variant: "destructive" })
                    : undefined
                }
                onClick={onConfirm}
              >
                {options.ok}
              </AlertDialogAction>
            </AlertDialogFooter>
          </>
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}
