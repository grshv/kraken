import { Badge } from "@/components/ui/badge";
const labels: Record<string, string> = {
  up: "Работает",
  down: "Недоступен",
  unknown: "Нет данных",
  degraded: "Нужна проверка",
  pending: "Принято",
  accepted: "Принято",
  staged: "Подготовлено",
  draft: "Черновик",
  validating: "Проверка",
  validated: "Проверено",
  promoting: "Применение",
  reloading: "Применение",
  verifying: "Проверка применения",
  succeeded: "Применено",
  rejected: "Отклонено",
  rolling_back: "Откат",
  rolled_back: "Восстановлена предыдущая версия",
};

export function StatusBadge({ value }: { value: string }) {
  return (
    <Badge
      variant="outline"
      className={`pill ${value === "up" || value === "succeeded" || value === "validated" ? "good" : value === "down" || value === "rejected" || value === "degraded" ? "bad" : "neutral"}`}
    >
      <i />
      {labels[value] || value}
    </Badge>
  );
}
