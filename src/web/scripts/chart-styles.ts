import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ChartStyle, type ChartConfig } from "@/components/ui/chart";
import { TRAFFIC_CHART_ID, trafficChartConfig } from "@/clients/TrafficChart";
import { SUMMARY_CHART_ID, summaryChartConfig } from "@/log/TrafficSummary";

// chartStyles renders the colour <style> of every chart id in the panel, as
// shadcn's ChartContainer inserts it.
export function chartStyles(): Record<string, string> {
  const charts: [string, ChartConfig][] = [
    [TRAFFIC_CHART_ID, trafficChartConfig],
    [SUMMARY_CHART_ID, summaryChartConfig],
  ];
  const out: Record<string, string> = {};
  for (const [id, config] of charts) {
    const html = renderToStaticMarkup(
      createElement(ChartStyle, { id: `chart-${id}`, config }),
    );
    const m = html.match(/^<style>([\s\S]*)<\/style>$/);
    if (!m) throw new Error(`chart ${id}: style not rendered`);
    out[`chart-${id}`] = m[1];
  }
  return out;
}
