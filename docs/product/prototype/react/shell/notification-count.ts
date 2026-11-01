type NoticeData = {
  denied: Record<string, {d: string; why: string}[]>;
  unknown: {ip: string}[];
};

// Считает события, а не число отказанных запросов; ключи совпадают с отметками мока.
export function newEventCount(data: NoticeData, marks: Record<string, string>, inScope: (id: string) => boolean, includeUnknown: boolean): number {
  let count = 0;
  for (const [id, rows] of Object.entries(data.denied)) {
    if (!inScope(id)) continue;
    count += rows.filter(row => row.why === 'list' && (marks[id+'|'+row.d] ?? 'new') === 'new').length;
  }
  if (includeUnknown) count += data.unknown.filter(row => (marks['source/'+row.ip] ?? 'new') === 'new').length;
  return count;
}
