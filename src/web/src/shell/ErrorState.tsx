import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";

// Fatal/empty surface when the API is unreachable (503 or transport loss)
// or the bootstrap session itself failed. Single retry button.
export function ErrorState({
  title,
  detail,
  onRetry,
}: {
  title: string;
  detail?: string;
  onRetry?: () => void;
}) {
  return (
    <div className="flex max-w-[460px] flex-col items-start gap-3">
      <AlertTriangle className="size-7 text-bad" aria-hidden />
      <h1 className="text-xl">{title}</h1>
      {detail && <p role="alert">{detail}</p>}
      {onRetry && <Button onClick={onRetry}>Повторить</Button>}
    </div>
  );
}
