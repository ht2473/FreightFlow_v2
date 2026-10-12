import type { MapGeoJSONFeature } from "maplibre-gl";
import { useState } from "react";
import { num } from "@/shared/format";
import { EmptyState, TextField } from "@/shared/ui";
import s from "./map.module.css";
import { m } from "./messages";

export interface ObjectItem {
  id: string | number | undefined;
  name?: string;
  type: string;
  area?: number;
  lngLat: [number, number];
  feature: MapGeoJSONFeature;
}

/** Подписи типов объектов в списке. */
const typeTitles: Record<string, string> = {
  logistics_center: "Логистический центр",
  warehouse: "Склад",
  cold_storage: "Холодильный склад",
  terminal: "Грузовой терминал",
  sorting_center: "Сортировочный центр",
  wholesale: "Оптовая торговля",
  rail_freight: "Ж/д грузовой объект",
  port: "Речной порт",
};

const limit = 200;

/** Объекты логистики в видимой области карты, крупные сверху. */
export function ObjectsPanel({
  items,
  layerOn,
  zoom,
  selectedId,
  onSelect,
}: {
  items: ObjectItem[];
  layerOn: boolean;
  zoom: number;
  selectedId: string | number | undefined;
  onSelect: (o: ObjectItem) => void;
}) {
  const [query, setQuery] = useState("");
  if (!layerOn) {
    return <EmptyState title={m.objectsHidden}>{m.objectsHiddenHint}</EmptyState>;
  }
  if (zoom < 8) {
    return <EmptyState title={m.objectsZoomIn}>{m.objectsZoomInHint}</EmptyState>;
  }
  const q = query.trim().toLocaleLowerCase("ru");
  const list = q ? items.filter((o) => (o.name ?? "").toLocaleLowerCase("ru").includes(q)) : items;
  return (
    <div className={s.objects}>
      <TextField label={m.search} placeholder={m.searchPlaceholder} value={query} onChange={setQuery} type="search" />
      <div className={s.sectionTitle}>
        {m.objectsInView} · <span className="num">{num(list.length)}</span>
      </div>
      {list.length === 0 ? (
        <EmptyState title={m.objectsNone}>{m.objectsNoneHint}</EmptyState>
      ) : (
        <ul className={s.objectList}>
          {list.slice(0, limit).map((o) => (
            <li key={String(o.id)}>
              <button
                type="button"
                className={o.id === selectedId ? `${s.objectItem} ${s.objectSelected}` : s.objectItem}
                onClick={() => onSelect(o)}
              >
                <span className={s.objectName}>{o.name ?? m.noName}</span>
                <span className={s.objectMeta}>
                  {typeTitles[o.type] ?? o.type}
                  {o.area ? (
                    <>
                      {" · "}
                      <span className="num">{num(o.area)}</span> м²
                    </>
                  ) : null}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
