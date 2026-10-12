import type { ErrorComponentProps } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { isProblem, problemMessage } from "@/shared/api/client";
import { Button, EmptyState, Skeleton } from "@/shared/ui";
import s from "./pages.module.css";

/** Ошибка при отрисовке или загрузке страницы: объяснение и повтор без перезагрузки всего приложения. */
export function ErrorPage({ error, reset }: ErrorComponentProps) {
  const chunkFailed =
    error instanceof TypeError && /dynamically imported module|Importing a module/i.test(error.message);
  return (
    <EmptyState
      icon={<TriangleAlert size={20} strokeWidth={1.7} />}
      title={chunkFailed ? "Вышла новая версия" : "Не удалось показать страницу"}
      action={
        chunkFailed ? (
          <Button variant="primary" onPress={() => window.location.reload()}>
            Обновить страницу
          </Button>
        ) : (
          <Button onPress={reset}>Повторить</Button>
        )
      }
    >
      {chunkFailed
        ? "Обновите страницу, чтобы загрузить её."
        : `${isProblem(error) ? problemMessage(error) : "Ошибка в интерфейсе."} Если она повторяется, сообщите администратору.`}
    </EmptyState>
  );
}

/** Заглушка на время загрузки страницы. */
export function PendingPage() {
  return (
    <div className={s.pending} role="status" aria-busy="true" aria-label="Загрузка">
      <Skeleton width={240} height={26} />
      <Skeleton width={420} height={14} />
      <Skeleton height={160} />
    </div>
  );
}
