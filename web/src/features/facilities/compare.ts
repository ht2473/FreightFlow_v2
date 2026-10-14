import { create } from "zustand";

/** Объектов в сравнении (как в API). */
export const maxCompare = 4;

const storageKey = "ff.facilities.compare";

function load(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey) ?? "[]");
    return Array.isArray(v) ? v.filter((x) => typeof x === "string").slice(0, maxCompare) : [];
  } catch {
    return [];
  }
}

function save(ids: string[]) {
  try {
    localStorage.setItem(storageKey, JSON.stringify(ids));
  } catch {
    // Без хранилища набор живёт до перезагрузки.
  }
}

/** Набор объектов для сравнения: добавляются из карточки, сравниваются на отдельной странице. */
export const useCompare = create<{
  ids: string[];
  toggle: (id: string) => void;
  clear: () => void;
}>((set) => ({
  ids: load(),
  toggle: (id) =>
    set((st) => {
      const ids = st.ids.includes(id)
        ? st.ids.filter((x) => x !== id)
        : st.ids.length < maxCompare
          ? [...st.ids, id]
          : st.ids;
      save(ids);
      return { ids };
    }),
  clear: () => {
    save([]);
    set({ ids: [] });
  },
}));
