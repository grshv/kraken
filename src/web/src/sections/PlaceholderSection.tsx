import type { ReactNode } from "react";
import { EmptyState } from "../shell/EmptyState";

// Stub for sections built in slice 12. Keeps the list/detail layout so the
// resizer and shell rhythm are exercised even before the screen exists.
export function PlaceholderSection({
  title,
  icon,
  note = "Раздел появится в следующем срезе.",
}: {
  title: string;
  icon: ReactNode;
  note?: string;
}) {
  return (
    <div className="placeholder-section">
      <aside className="list-pane">
        <div className="list-head">
          <span className="list-title">{title}</span>
        </div>
        <div className="list-body">
          <div className="list-muted">Список появится позже</div>
        </div>
      </aside>
      <section className="inspector-pane">
        <div className="inspector-head">
          <h1 className="page-title">{title}</h1>
        </div>
        <div className="inspector-body">
          <EmptyState icon={icon} title={note} />
        </div>
      </section>
    </div>
  );
}
