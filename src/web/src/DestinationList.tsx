import type { Destination } from "./model";

export function DestinationList({ items }: { items: Destination[] }) {
  const domain = (item: Destination, i: number) => (
    <li key={`${item.domain}-${i}`}>
      {item.domain}
      {item.include_subdomains && <small> и поддомены</small>}
    </li>
  );
  if (!items.length)
    return <span className="muted">Нет разрешённых доменов</span>;
  return (
    <div className="domain-list">
      <ul>{items.slice(0, 5).map(domain)}</ul>
      {items.length > 5 && (
        <details>
          <summary>Ещё {items.length - 5}</summary>
          <ul>{items.slice(5).map(domain)}</ul>
        </details>
      )}
    </div>
  );
}
