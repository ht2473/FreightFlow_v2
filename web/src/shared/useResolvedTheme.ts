import { useEffect, useState } from "react";
import { resolvedTheme, useTheme } from "./theme";

/** Действующая тема (светлая или тёмная) с учётом смены системной настройки. */
export function useResolvedTheme(): "light" | "dark" {
  const theme = useTheme((st) => st.theme);
  const [systemDark, setSystemDark] = useState(() => window.matchMedia("(prefers-color-scheme: dark)").matches);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const on = (e: MediaQueryListEvent) => setSystemDark(e.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return theme === "system" ? (systemDark ? "dark" : "light") : resolvedTheme(theme);
}
