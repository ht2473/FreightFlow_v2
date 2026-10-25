import { useNavigate } from "@tanstack/react-router";
import { fetchClient } from "@/shared/api/client";
import { type Hit, hitTarget } from "@/shared/search";
import { toast } from "@/shared/ui/toast";

/** Открытие найденного документа: страница сущности, карта с меткой или сохранённый вид. */
export function useOpenHit() {
  const navigate = useNavigate();
  return async (h: Hit) => {
    if (h.kind === "saved_view") {
      // Вид хранит параметры адреса страницы; их отдаёт список видов.
      for (const page of ["facilities", "map"] as const) {
        const { data } = await fetchClient.GET("/views", { params: { query: { page } } });
        const v = data?.find((x) => x.id === h.ref);
        if (v) {
          await navigate({ to: page === "map" ? "/map" : "/facilities", search: v.params as never });
          return;
        }
      }
      toast.error("Вид не найден: возможно, его удалили");
      return;
    }
    const t = hitTarget(h);
    if (t) {
      await navigate(t as never);
    }
  };
}
