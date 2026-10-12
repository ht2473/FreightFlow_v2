import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { $api, type Schemas } from "./api/client";
import { type Theme, useTheme } from "./theme";
import { toast } from "./ui/toast";

const settingsKey = ["get", "/me/settings", {}] as const;

/**
 * Тема из профиля пользователя. Профиль — источник истины; localStorage лишь
 * позволяет применить тему до отрисовки страницы.
 */
export function useThemeSync() {
  const settings = $api.useQuery("get", "/me/settings", {}, { staleTime: Number.POSITIVE_INFINITY });
  const serverTheme = settings.data?.theme;
  useEffect(() => {
    const { theme, setTheme } = useTheme.getState();
    if (serverTheme && serverTheme !== theme) {
      setTheme(serverTheme);
    }
  }, [serverTheme]);
}

/** Текущая тема и её смена с сохранением в профиле. */
export function useThemePreference() {
  const qc = useQueryClient();
  const theme = useTheme((st) => st.theme);
  const save = $api.useMutation("put", "/me/settings");

  const change = (next: Theme) => {
    const prev = useTheme.getState().theme;
    useTheme.getState().setTheme(next);
    qc.setQueryData<Schemas["Settings"]>(settingsKey, (old) => ({ ...old, theme: next }));
    save.mutate(
      { body: { theme: next } },
      {
        onError: () => {
          useTheme.getState().setTheme(prev);
          qc.setQueryData<Schemas["Settings"]>(settingsKey, (old) => ({ ...old, theme: prev }));
          toast.error("Не удалось сохранить тему");
        },
      },
    );
  };

  return { theme, change };
}
