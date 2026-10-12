import { create } from "zustand";
import s from "./ui.module.css";

interface ToastItem {
  id: number;
  text: string;
  error?: boolean;
}

interface ToastState {
  items: ToastItem[];
  push: (text: string, error?: boolean) => void;
  remove: (id: number) => void;
}

let nextId = 1;

const useToasts = create<ToastState>((set) => ({
  items: [],
  push: (text, error) => {
    const id = nextId++;
    set((st) => ({ items: [...st.items, { id, text, error }] }));
    setTimeout(() => set((st) => ({ items: st.items.filter((t) => t.id !== id) })), error ? 5000 : 2200);
  },
  remove: (id) => set((st) => ({ items: st.items.filter((t) => t.id !== id) })),
}));

/** Короткое уведомление внизу экрана. */
export const toast = {
  success: (text: string) => useToasts.getState().push(text),
  error: (text: string) => useToasts.getState().push(text, true),
};

/** Область уведомлений; объявляется экранным читалкам. */
export function ToastRegion() {
  const items = useToasts((st) => st.items);
  return (
    <div className={s.toastRegion} aria-live="polite">
      {items.map((t) => (
        <div
          key={t.id}
          className={t.error ? `${s.toast} ${s.toastError}` : s.toast}
          role={t.error ? "alert" : "status"}
        >
          {t.text}
        </div>
      ))}
    </div>
  );
}
