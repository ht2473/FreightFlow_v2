import { create } from "zustand";

export type Theme = "light" | "dark" | "system";

const storageKey = "ff-theme";

function readStored(): Theme {
  try {
    const v = localStorage.getItem(storageKey);
    if (v === "light" || v === "dark") {
      return v;
    }
  } catch {
    // Хранилище недоступно — системная тема.
  }
  return "system";
}

/** Применяет тему к документу и сохраняет её для SPA (localStorage) и серверных страниц (cookie). */
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "system") {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", theme);
  }
  try {
    if (theme === "system") {
      localStorage.removeItem(storageKey);
    } else {
      localStorage.setItem(storageKey, theme);
    }
  } catch {
    // Без хранилища тема действует до перезагрузки.
  }
  const maxAge = theme === "system" ? 0 : 31536000;
  // biome-ignore lint/suspicious/noDocumentCookie: одна простая cookie для серверных страниц
  document.cookie = `ff_theme=${theme}; path=/; max-age=${maxAge}; samesite=lax`;
}

interface ThemeState {
  theme: Theme;
  setTheme: (t: Theme) => void;
}

export const useTheme = create<ThemeState>((set) => ({
  theme: readStored(),
  setTheme: (theme) => {
    applyTheme(theme);
    set({ theme });
  },
}));

/** Действующая тема с учётом системной настройки. */
export function resolvedTheme(theme: Theme): "light" | "dark" {
  if (theme !== "system") {
    return theme;
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
