import { Map as MapLibreMap, type StyleSpecification, setWorkerUrl } from "maplibre-gl";
import workerUrl from "maplibre-gl/dist/maplibre-gl-worker.mjs?url";
import "maplibre-gl/dist/maplibre-gl.css";

// Воркер — отдельным файлом с нашего сервера: при предварительной сборке зависимостей
// (Vite) относительный адрес воркера внутри MapLibre теряется.
setWorkerUrl(workerUrl);

let map: MapLibreMap | null = null;
let container: HTMLDivElement | null = null;
let styleReady = false;

/** Москва: центр и масштаб по умолчанию. */
export const DEFAULT_VIEW = { center: [37.62, 55.75] as [number, number], zoom: 9.6 };

/**
 * Единственный экземпляр карты рабочего пространства. Контейнер создаётся один раз и
 * переносится в элемент страницы; при уходе со страницы карта не уничтожается и сохраняет
 * загруженные тайлы.
 */
export function mountMap(
  host: HTMLElement,
  init: { center: [number, number]; zoom: number; style: StyleSpecification },
): MapLibreMap {
  if (!map || !container) {
    container = document.createElement("div");
    container.className = "ff-map";
    host.appendChild(container);
    const created = new MapLibreMap({
      container,
      style: init.style,
      center: init.center,
      zoom: init.zoom,
      minZoom: 5,
      maxZoom: 18,
      // Плоская 2D-карта: без поворота и наклона.
      dragRotate: false,
      pitchWithRotate: false,
      touchPitch: false,
      attributionControl: false,
      locale: { "Map.Title": "Карта" },
    });
    created.touchZoomRotate.disableRotation();
    created.keyboard.disableRotation();
    created.once("style.load", () => {
      styleReady = true;
    });
    map = created;
    return created;
  }
  host.appendChild(container);
  map.jumpTo({ center: init.center, zoom: init.zoom });
  map.resize();
  return map;
}

/** Отсоединяет карту от страницы. */
export function unmountMap() {
  container?.remove();
}

/**
 * Выполняет действие, когда первый стиль карты загружен (сравнение стилей до этого
 * невозможно). Возвращает отмену ожидания.
 */
export function onStyleReady(m: MapLibreMap, fn: () => void): () => void {
  if (styleReady) {
    fn();
    return () => {};
  }
  m.once("style.load", fn);
  return () => {
    m.off("style.load", fn);
  };
}
