import type { ReactNode } from "react";
import s from "./alerts.module.css";

/** Группа флажков с подписью для чтения с экрана. */
export function CheckGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <fieldset className={s.group}>
      <legend className="visually-hidden">{label}</legend>
      {children}
    </fieldset>
  );
}
