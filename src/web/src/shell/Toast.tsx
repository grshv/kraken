import { useEffect } from "react";

// Tiny toast: auto-dismisses after the given timeout. Rendered once at the
// shell root; the App holds the message string and clears it on timeout.
export function Toast({
  message,
  onDismiss,
  timeout = 2600,
}: {
  message: string;
  onDismiss: () => void;
  timeout?: number;
}) {
  useEffect(() => {
    if (!message) return;
    const t = setTimeout(onDismiss, timeout);
    return () => clearTimeout(t);
  }, [message, onDismiss, timeout]);
  if (!message) return null;
  return (
    <div className="toast" role="status" aria-live="polite">
      {message}
    </div>
  );
}
