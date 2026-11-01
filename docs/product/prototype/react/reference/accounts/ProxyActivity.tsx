import {Timestamp} from '@/components/kraken/timestamp';
import { useEffect, useState } from "react";
import { TriangleAlert } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Callout, Kpi, KpiGroup } from "@/components/kraken/blocks";
import { ago } from "../model";
import type { SectionProps } from "../sections/sectionProps";

type Activity = {
  window_hours: number;
  requests: number;
  denied: number;
  failed_logins: number;
  last_used_at: string | null;
  sources: {
    source_ip: string;
    requests: number;
    failed_logins: number;
    last_seen: string;
  }[];
};

const num = (n: number) => n.toLocaleString("ru-RU");

// useProxyActivity loads GET /proxy-accounts/{name}/activity (24 h from the
// access log: requests, wrong passwords, source addresses).
export function useProxyActivity(
  request: SectionProps["request"],
  username: string,
) {
  const [activity, setActivity] = useState<Activity | null>(null);
  useEffect(() => {
    let live = true;
    setActivity(null);
    request<Activity>(
      `/proxy-accounts/${encodeURIComponent(username)}/activity`,
    )
      .then((a) => live && setActivity(a))
      .catch(() => live && setActivity(null));
    return () => {
      live = false;
    };
  }, [request, username]);
  return activity;
}

// ProxyKpis: requests, wrong passwords, policy denials and addresses.
export function ProxyKpis({ activity }: { activity: Activity | null }) {
  const v = (n?: number) => (activity ? num(n ?? 0) : "—");
  return (
    <KpiGroup>
      <Kpi
        label="Запросов, 24 ч"
        value={v(activity?.requests)}
        hint={
          activity?.last_used_at
            ? <span>Последний: <Timestamp value={activity.last_used_at} mode="relative"/></span>
            : undefined
        }
      />
      <Kpi
        label="Неверный пароль, 24 ч"
        value={v(activity?.failed_logins)}
        tone={activity?.failed_logins ? "bad" : undefined}
      />
      <Kpi label="Отказов политики, 24 ч" value={v(activity?.denied)} />
      <Kpi label="Адресов" value={v(activity?.sources.length)} />
    </KpiGroup>
  );
}

// ProxySources: "С каких адресов" with a hint when an address keeps sending a
// wrong password (often a device that still has the old one).
export function ProxySources({ activity }: { activity: Activity | null }) {
  const failing = (activity?.sources ?? []).filter((s) => s.failed_logins > 0);
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>С каких адресов</CardTitle>
          <CardDescription>За 24 ч, по последнему обращению</CardDescription>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {activity && activity.sources.length === 0 ? (
          <span className="text-sm text-muted-foreground">
            Логином не пользовались
          </span>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Адрес</TableHead>
                <TableHead className="text-right">Запросов</TableHead>
                <TableHead className="text-right">Неверный пароль</TableHead>
                <TableHead className="text-right">Последний</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(activity?.sources ?? []).map((s) => (
                <TableRow key={s.source_ip}>
                  <TableCell className="font-mono">{s.source_ip}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {num(s.requests)}
                  </TableCell>
                  <TableCell
                    className={
                      "text-right tabular-nums" +
                      (s.failed_logins ? " text-bad" : "")
                    }
                  >
                    {num(s.failed_logins)}
                  </TableCell>
                  <TableCell className="text-right text-muted-foreground">
                    <Timestamp value={s.last_seen} mode="relative"/>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {failing.length > 0 && (
          <Callout tone="warn" icon={<TriangleAlert aria-hidden />}>
            <span className="text-sm">
              С {failing[0].source_ip} было {num(failing[0].failed_logins)}{" "}
              попыток с неверным паролем
              {failing.length > 1 ? ` (и ещё ${failing.length - 1} адр.)` : ""}.
              Возможно, на устройстве сохранён прежний пароль.
            </span>
          </Callout>
        )}
      </CardContent>
    </Card>
  );
}
