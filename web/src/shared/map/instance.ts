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
 * Пределы перемещения карты — рамка подложки (Москва и агломерация внутри ЦКАД, basemap.BBox):
 * за ней нет ни подложки, ни данных.
 */
export const MOSCOW_BOUNDS: [[number, number], [number, number]] = [
  [36.3, 54.9],
  [39.0, 56.55],
];

/** Наименьший масштаб, если окно карты ещё не размечено. */
export const MIN_ZOOM = 8;

/** Доля ширины мира в проекции Меркатора: долгота → [0, 1]. */
const mercX = (lon: number) => (lon + 180) / 360;
/** Доля высоты мира в проекции Меркатора: широта → [0, 1] (сверху вниз). */
const mercY = (lat: number) => {
  const r = (lat * Math.PI) / 180;
  return (1 - Math.log(Math.tan(Math.PI / 4 + r / 2)) / Math.PI) / 2;
};

/**
 * Масштаб, при котором рамка подложки закрывает окно карты целиком. Если окно больше рамки,
 * MapLibre не может удержать её края и показывает пустое поле за ними — поэтому наименьший
 * масштаб зависит от размера окна.
 */
export function coverZoom(width: number, height: number, b = MOSCOW_BOUNDS): number {
  const [[w, s], [e, n]] = b;
  const zx = Math.log2(width / (512 * (mercX(e) - mercX(w))));
  const zy = Math.log2(height / (512 * (mercY(s) - mercY(n))));
  return Math.max(MIN_ZOOM, zx, zy);
}

/** Держит карту в рамке подложки: пределы перемещения и наименьший масштаб по размеру окна. */
export function lockToBounds(m: MapLibreMap): void {
  const update = () => {
    const c = m.getContainer();
    if (c.clientWidth > 0 && c.clientHeight > 0) {
      m.setMinZoom(Math.min(coverZoom(c.clientWidth, c.clientHeight), 12));
    }
  };
  m.setMaxBounds(MOSCOW_BOUNDS);
  update();
  m.on("resize", update);
}

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
      minZoom: MIN_ZOOM,
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
    lockToBounds(created);
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
