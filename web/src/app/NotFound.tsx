import { Link } from "@tanstack/react-router";
import { Compass } from "lucide-react";
import { EmptyState } from "@/shared/ui";

export function NotFound() {
  return (
    <EmptyState
      icon={<Compass size={20} strokeWidth={1.7} />}
      title="Страница не найдена"
      action={
        <Link to="/dashboard" className="btn">
          На главную
        </Link>
      }
    >
      Проверьте адрес или выберите раздел в меню.
    </EmptyState>
  );
}
