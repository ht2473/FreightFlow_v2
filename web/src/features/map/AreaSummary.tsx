import type { Polygon } from "geojson";
import { X } from "lucide-react";
import { $api, problemMessage } from "@/shared/api/client";
import { num } from "@/shared/format";
import { IconButton, Skeleton } from "@/shared/ui";
import { area } from "../facilities/labels";
import s from "./map.module.css";

/** Сводка по выделенной области: объекты, площади, инфраструктура, ограничения, работы. */
export function AreaSummary({
  polygon,
  onOpen,
  onClose,
}: {
  polygon: Polygon;
  onOpen: (facilityId: string) => void;
  onClose: () => void;
}) {
  const q = $api.useQuery("post", "/facilities/area", {
    body: { polygon: polygon as unknown as Record<string, never> },
  });
  const a = q.data;
  return (
    <section className={`${s.float} ${s.areaPanel}`} aria-label="Сводка по области">
      <header className={s.areaHead}>
        <h2>Сводка по области</h2>
        <IconButton label="Закрыть сводку" onPress={onClose}>
          <X size={16} />
        </IconButton>
      </header>
      {q.isPending ? (
        <Skeleton height={120} />
      ) : q.error || !a ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(q.error)}
        </div>
      ) : (
        <>
          <dl className={s.areaFacts}>
            <dt>Площадь области</dt>
            <dd className="num">{num(a.areaKm2, a.areaKm2 < 10 ? 2 : 0)} км²</dd>
            <dt>Объектов реестра</dt>
            <dd className="num">{num(a.facilities)}</dd>
            <dt>Площадь застройки</dt>
            <dd className="num">{area(a.buildingAreaM2)}</dd>
            <dt>Общая площадь (оценка)</dt>
            <dd className="num">{area(a.floorAreaM2)}</dd>
            <dt>Участков с ограничениями</dt>
            <dd className="num">
              {num(a.restrictions)} · {num(a.restrictedKm, 1)} км
            </dd>
            <dt>Дорожные работы</dt>
            <dd className="num">
              идут {num(a.worksActive)}, запланировано {num(a.worksPlanned)}
            </dd>
          </dl>
          {a.types.length > 0 && (
            <p className={s.areaLine}>{a.types.map((t) => `${t.label}: ${num(t.count)}`).join(" · ")}</p>
          )}
          {a.poi.length > 0 && (
            <p className={s.areaLine}>{a.poi.map((t) => `${t.label}: ${num(t.count)}`).join(" · ")}</p>
          )}
          {a.top.length > 0 && (
            <>
              <h3 className={s.areaSub}>Крупнейшие объекты</h3>
              <ul className={s.areaList}>
                {a.top.map((f) => (
                  <li key={f.id}>
                    <button type="button" onClick={() => onOpen(f.id)}>
                      {f.name ?? f.typeName}
                    </button>
                    <span className="num">{area(f.buildingAreaM2)}</span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </section>
  );
}
