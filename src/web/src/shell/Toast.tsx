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
    <div
      className="fixed bottom-16 left-1/2 z-[60] -translate-x-1/2 rounded-md bg-primary px-3.5 py-2 text-base text-primary-foreground shadow-[0_6px_20px_#0003]"
      role="status"
      aria-live="polite"
    >
      {message}
    </div>
  );
}
