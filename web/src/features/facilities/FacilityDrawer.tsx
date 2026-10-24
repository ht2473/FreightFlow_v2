import { Link } from "@tanstack/react-router";
import { ExternalLink, GitCompare, Map as MapIcon, Maximize2, Route, Timer } from "lucide-react";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { date, dateTime, num } from "@/shared/format";
import { Badge, Button, cellClass, EmptyState, Skeleton, tableClass } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { Drawer } from "@/shared/ui/overlays";
import { formatCenter } from "../map/state";
import { restrictionText } from "../network/labels";
import { CurateFacility, fieldLabel, History, ReportIssue } from "./Curation";
import { maxCompare, useCompare } from "./compare";
import s from "./facilities.module.css";
import {
  area,
  confidenceLevel,
  distance,
  osmUrl,
  poiLabel,
  ringLabel,
  sourceLabel,
  targetShort,
  workKindLabel,
} from "./labels";

type Card = Schemas["FacilityCard"];

/** Ящик карточки объекта: обзор, доступность, ограничения, работы, инфраструктура, источники. */
export function FacilityDrawer({ id, onClose }: { id: string | undefined; onClose: () => void }) {
  const card = $api.useQuery(
    "get",
    "/facilities/{facilityId}",
    { params: { path: { facilityId: id ?? "" } } },
    { enabled: !!id },
  );
  const c = card.data;
  const f = c?.facility;
  return (
    <Drawer
      isOpen={!!id}
      onOpenChange={(open) => !open && onClose()}
      title={f ? (f.name ?? f.typeName) : "Объект"}
      subtitle={
        f && (
          <span className={s.drawerTags}>
            <Badge tone="info">{f.typeName}</Badge>
            <Badge tone={confidenceLevel(f.confidence).tone}>достоверность {confidenceLevel(f.confidence).label}</Badge>
          </span>
        )
      }
      footer={f && <Actions f={f} />}
    >
      {card.isPending ? (
        <Skeleton height={160} />
      ) : card.error || !c ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(card.error)}
        </div>
      ) : (
        <>
          <CardTabs c={c} />
          <Link to="/facilities/$facilityId" params={{ facilityId: c.facility.id }} className={s.fullLink}>
            <Maximize2 size={13} aria-hidden /> Открыть карточку полностью
          </Link>
        </>
      )}
    </Drawer>
  );
}

/** Вкладки карточки — в ящике и на странице объекта. */
export function CardTabs({ c }: { c: Card }) {
  return (
    <Tabs
      label="Разделы карточки"
      items={[
        { id: "overview", title: "Обзор", content: <Overview c={c} /> },
        { id: "access", title: "Доступность", content: <Access c={c} /> },
        { id: "restrictions", title: `Ограничения (${c.restrictions.length})`, content: <Restrictions c={c} /> },
        { id: "works", title: `Работы (${c.works.length})`, content: <Works c={c} /> },
        { id: "nearby", title: "Рядом", content: <Nearby c={c} /> },
        { id: "sources", title: "Источники", content: <Sources c={c} /> },
        { id: "history", title: "История", content: <History c={c} /> },
      ]}
    />
  );
}

/** Действия карточки: маршрут и изохроны от объекта, сравнение, карта. */
export function Actions({ f }: { f: Schemas["FacilityItem"] }) {
  const compare = useCompare();
  const inCompare = compare.ids.includes(f.id);
  const from = formatCenter(f.point.lon, f.point.lat);
  const name = f.name ?? f.typeName;
  return (
    <div className={s.actions}>
      <Link to="/network" search={{ mode: "route", from, name }} className="btn btn-sm">
        <Route size={14} aria-hidden /> Маршрут отсюда
      </Link>
      <Link to="/network" search={{ mode: "isochrone", from, name }} className="btn btn-sm">
        <Timer size={14} aria-hidden /> Изохроны
      </Link>
      <Button
        size="sm"
        isDisabled={!inCompare && compare.ids.length >= maxCompare}
        onPress={() => compare.toggle(f.id)}
      >
        <GitCompare size={14} aria-hidden /> {inCompare ? "Убрать из сравнения" : "Сравнить"}
      </Button>
      <Link to="/map" search={{ c: from, z: 16, l: "facility_areas,facilities,restrictions" }} className="btn btn-sm">
        <MapIcon size={14} aria-hidden /> На карте
      </Link>
    </div>
  );
}

function Overview({ c }: { c: Card }) {
  const f = c.facility;
  const me = useMe();
  const mkad = c.access.find((a) => a.profile === "truck_20" && a.target === "mkad");
  return (
    <div className={s.stack}>
      {c.curated.length > 0 && (
        <p className={s.curatedNote}>
          Уточнено редактором: {c.curated.map((x) => fieldLabel[x].toLowerCase()).join(", ")}
        </p>
      )}
      <dl className={s.facts}>
        <dt>Адрес</dt>
        <dd>{f.address ?? "—"}</dd>
        <dt>Территория</dt>
        <dd>{[f.okrug, f.district, f.ring && ringLabel[f.ring]].filter(Boolean).join(", ") || "—"}</dd>
        <dt>Площадь застройки</dt>
        <dd className="num">
          {area(f.buildingAreaM2)}
          {f.buildings > 0 && ` · зданий ${num(f.buildings)}`}
          {c.levels !== undefined && ` · этажей ${num(c.levels)}`}
        </dd>
        <dt>Общая площадь</dt>
        <dd className="num">
          {area(f.floorAreaM2)} <Badge plain>модель</Badge>
        </dd>
        <dt>Территория объекта</dt>
        <dd className="num">{area(f.territoryAreaM2)}</dd>
        <dt>До МКАД (20 т)</dt>
        <dd className="num">{mkad?.minutes !== undefined ? `${num(mkad.minutes)} мин, ${num(mkad.km, 1)} км` : "—"}</dd>
        <dt>В реестре</dt>
        <dd>
          с {date(f.firstSeen)}, обновлён {dateTime(c.updatedAt)}
          {c.status !== "active" && (
            <Badge tone="warn">{c.status === "missing" ? "нет в источнике" : "исключён"}</Badge>
          )}
        </dd>
      </dl>
      <div>
        <h3 className={s.subhead}>Достоверность {num(f.confidence * 100)} %</h3>
        <ul className={s.reasons}>
          {c.confidenceReasons.map((r) => (
            <li key={r.code}>
              <span>{r.text}</span>
              <span className={`${s.muted} num`}>+{num(r.weight * 100)}</span>
            </li>
          ))}
        </ul>
      </div>
      <div className={s.actions}>
        {can(me.data, "registry.curate") && <CurateFacility c={c} />}
        <ReportIssue c={c} />
      </div>
    </div>
  );
}

function Access({ c }: { c: Card }) {
  if (c.access.length === 0) {
    return <EmptyState title="Доступность не рассчитана">Расчёт идёт после сборки графа маршрутизации.</EmptyState>;
  }
  const profiles = [...new Map(c.access.map((a) => [a.profile, a.profileName])).entries()];
  const targets = (["mkad", "ttk", "center", "rail_freight"] as const).filter((t) =>
    c.access.some((a) => a.target === t),
  );
  return (
    <div className={s.stack}>
      <table className={tableClass}>
        <thead>
          <tr>
            <th>Профиль ТС</th>
            {targets.map((t) => (
              <th key={t} className={cellClass.right}>
                {targetShort[t]}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {profiles.map(([code, name]) => (
            <tr key={code}>
              <td>{name}</td>
              {targets.map((t) => {
                const a = c.access.find((x) => x.profile === code && x.target === t);
                return (
                  <td key={t} className={`${cellClass.right} num`}>
                    {a?.minutes !== undefined ? num(a.minutes) : "нет пути"}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      <p className={s.muted}>
        Минуты в пути по сети до ближайшей точки: МКАД, ТТК, центра (Красная площадь), ж/д грузового пункта — с
        ограничениями участков для профиля; зоны пропусков не учитываются.
      </p>
    </div>
  );
}

function Restrictions({ c }: { c: Card }) {
  if (c.restrictions.length === 0) {
    return <EmptyState title="Ограничений в 500 м нет" />;
  }
  return (
    <ul className={s.list}>
      {c.restrictions.map((r) => (
        <li key={`${r.kind}-${r.street}-${r.value}-${r.condition}`}>
          <div>
            <div>
              {restrictionText({ kind: r.kind, value: r.value, street: "", origins: [], segments: 1, lengthM: 0 })}
            </div>
            <div className={s.muted}>
              {[r.street, r.condition, r.origins.includes("sign") ? "знак" : "OSM"].filter(Boolean).join(" · ")}
            </div>
          </div>
          <span className={`${s.muted} num`}>{distance(r.distanceM)}</span>
        </li>
      ))}
    </ul>
  );
}

function Works({ c }: { c: Card }) {
  if (c.works.length === 0) {
    return <EmptyState title="Работ рядом нет">Идущие и запланированные работы в 500 м от объекта.</EmptyState>;
  }
  return (
    <ul className={s.list}>
      {c.works.map((w) => (
        <li key={w.id}>
          <div>
            <div>{w.title}</div>
            <div className={s.muted}>
              {workKindLabel[w.kind]} · {w.state === "active" ? "идут" : "запланированы"}
              {w.starts && ` · ${date(w.starts)}`}
              {w.ends && ` — ${date(w.ends)}`}
              {w.closure && " · перекрытие"}
            </div>
          </div>
          <span className={`${s.muted} num`}>{distance(w.distanceM)}</span>
        </li>
      ))}
    </ul>
  );
}

function Nearby({ c }: { c: Card }) {
  if (c.nearby.length === 0) {
    return <EmptyState title="Инфраструктуры в 3 км нет" />;
  }
  return (
    <ul className={s.list}>
      {c.nearby
        .slice()
        .sort((a, b) => a.distanceM - b.distanceM)
        .map((p) => (
          <li key={p.kind}>
            <div>
              <div>{poiLabel[p.kind] ?? p.kind}</div>
              {p.name && <div className={s.muted}>{p.name}</div>}
            </div>
            <span className={`${s.muted} num`}>{distance(p.distanceM)}</span>
          </li>
        ))}
    </ul>
  );
}

function Sources({ c }: { c: Card }) {
  return (
    <ul className={s.list}>
      <li>
        <div>
          <div>OpenStreetMap</div>
          <div className={s.muted}>контур, тип, название; © участники OpenStreetMap</div>
        </div>
        <a href={osmUrl(c.osmType, c.osmId)} target="_blank" rel="noreferrer" className={s.external}>
          {c.osmType}
          {c.osmId} <ExternalLink size={13} aria-hidden />
        </a>
      </li>
      {c.sources.map((src) => (
        <li key={`${src.relation}-${src.ref}`}>
          <div>
            <div>{sourceLabel[src.relation]}</div>
            <div className={s.muted}>{sourceDetails(src)}</div>
          </div>
          <span className={`${s.muted} num`}>{src.ref}</span>
        </li>
      ))}
    </ul>
  );
}

function sourceDetails(src: Schemas["FacilitySource"]): string {
  const d = src.details as Record<string, unknown>;
  const text = (k: string) => (typeof d[k] === "string" ? (d[k] as string) : undefined);
  if (src.relation === "address") {
    return [text("address"), text("obj_type")].filter(Boolean).join(" · ");
  }
  return [text("name"), text("number") && `№ ${text("number")}`, text("date") && date(text("date"))]
    .filter(Boolean)
    .join(" · ");
}
