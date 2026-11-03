import { useEffect, useState } from "react";
import type { SectionProps } from "../sections/sectionProps";
import { Journal } from "./Journal";
import { decodeState, type JournalState } from "./filters";

// LogSection: the journal screen. A "log:<state>" intent (a client's KPI, the
// palette's domain search, the client tab's link) opens it on that state.
export function LogSection(props: SectionProps) {
  const { intent, onIntentHandled } = props;
  const [opening, setOpening] = useState<JournalState | undefined>();
  useEffect(() => {
    if (!intent?.startsWith("log:")) return;
    const s = decodeState(intent.slice("log:".length));
    if (s) setOpening(s);
    onIntentHandled?.();
  }, [intent, onIntentHandled]);
  return (
    <Journal
      {...props}
      snap={props.draft?.snapshot ?? props.active}
      initial={opening}
    />
  );
}
