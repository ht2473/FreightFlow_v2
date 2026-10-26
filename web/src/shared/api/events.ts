import { useEffect, useRef } from "react";

/** События сервера, которые получает браузер (GET /api/v1/events). */
export const serverEventTypes = ["data.published", "job.progress", "scenario.computed"] as const;

/**
 * Вид события: событие сервера или `reconnected` — соединение восстановлено после разрыва,
 * события за время разрыва не досылаются, данные нужно перечитать.
 */
export type ServerEventType = (typeof serverEventTypes)[number] | "reconnected";

type Handler = (data: unknown) => void;

const listeners = new Map<ServerEventType, Set<Handler>>();
let source: EventSource | null = null;
let users = 0;

function emit(type: ServerEventType, data: unknown) {
  for (const h of listeners.get(type) ?? []) {
    h(data);
  }
}

function connect() {
  const es = new EventSource("/api/v1/events");
  let broken = false;
  es.addEventListener("error", () => {
    broken = true;
  });
  es.addEventListener("open", () => {
    if (broken) {
      broken = false;
      emit("reconnected", null);
    }
  });
  for (const type of serverEventTypes) {
    es.addEventListener(type, (e) => {
      try {
        emit(type, JSON.parse((e as MessageEvent<string>).data));
      } catch {
        // Кадр без JSON — не наш формат, пропускаем.
      }
    });
  }
  source = es;
}

/**
 * Подписка на событие сервера на время жизни компонента. Соединение одно на приложение:
 * открывается с первым подписчиком и закрывается с последним.
 */
export function useServerEvent(type: ServerEventType, handler: Handler) {
  const latest = useRef(handler);
  latest.current = handler;
  useEffect(() => {
    const h: Handler = (data) => latest.current(data);
    let set = listeners.get(type);
    if (!set) {
      set = new Set();
      listeners.set(type, set);
    }
    set.add(h);
    users++;
    if (!source) {
      connect();
    }
    return () => {
      set.delete(h);
      users--;
      if (users === 0 && source) {
        source.close();
        source = null;
      }
    };
  }, [type]);
}
