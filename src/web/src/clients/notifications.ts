import { useCallback, useEffect, useState } from "react";
import type { SectionProps } from "../sections/sectionProps";

// Computed on the server from the last 7 days of statistics; only the shared
// mark (seen / ignored) is stored. See GET /notifications.
export type NoticeStatus = "new" | "seen" | "ignored";

export type Notice = {
  id: string;
  kind: "denied_domain" | "unknown_source";
  status: NoticeStatus;
  client_id: string | null;
  client_name: string | null;
  source_ip: string | null;
  host: string | null;
  requests: number;
  denied: number;
  last_seen: string;
  marked_by: string | null;
  marked_at: string | null;
};

export type NoticeCounts = Record<NoticeStatus, number>;

// useClientNotices loads every denied-domain notification of one client
// (all statuses; the UI filters locally) and exposes a bulk mark.
export function useClientNotices(
  request: SectionProps["request"],
  clientID: string,
) {
  const [items, setItems] = useState<Notice[] | null>(null);
  const load = useCallback(() => {
    return request<{ items: Notice[] }>(
      `/notifications?status=all&kind=denied_domain&client_id=${encodeURIComponent(clientID)}`,
    )
      .then((r) => setItems(r.items ?? []))
      .catch(() => setItems((v) => v ?? []));
  }, [request, clientID]);
  useEffect(() => {
    setItems(null);
    void load();
  }, [load]);
  const mark = useCallback(
    async (ids: string[], status: NoticeStatus) => {
      if (!ids.length) return;
      await request("/notifications/marks", "POST", { ids, status });
      await load();
    },
    [request, load],
  );
  return { items, mark, reload: load };
}

export function countNotices(items: Notice[] | null): NoticeCounts {
  const c: NoticeCounts = { new: 0, seen: 0, ignored: 0 };
  for (const n of items ?? []) c[n.status]++;
  return c;
}

// useSourceNotices loads the marks of undescribed sources, keyed by IP, so
// the clients list can hide ignored ones and the "create from source" form
// can mark one seen or ignored.
export function useSourceNotices(request: SectionProps["request"]) {
  const [byIP, setByIP] = useState<Record<string, Notice>>({});
  const load = useCallback(() => {
    return request<{ items: Notice[] }>(
      "/notifications?status=all&kind=unknown_source",
    )
      .then((r) => {
        const m: Record<string, Notice> = {};
        for (const n of r.items ?? []) if (n.source_ip) m[n.source_ip] = n;
        setByIP(m);
      })
      .catch(() => {});
  }, [request]);
  useEffect(() => {
    void load();
  }, [load]);
  const mark = useCallback(
    async (ip: string, status: NoticeStatus) => {
      await request("/notifications/marks", "POST", {
        ids: [`source/${ip}`],
        status,
      });
      await load();
    },
    [request, load],
  );
  return { byIP, mark };
}
