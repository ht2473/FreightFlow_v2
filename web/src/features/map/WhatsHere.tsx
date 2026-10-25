import { Link } from "@tanstack/react-router";
import { Route, X } from "lucide-react";
import { $api } from "@/shared/api/client";
import { num } from "@/shared/format";
import { IconButton, Skeleton } from "@/shared/ui";
import s from "./map.module.css";

/** «Что здесь»: ближайший адрес в 50 м, район и координаты места на карте. */
export function WhatsHere({ point, onClose }: { point: [number, number]; onClose: () => void }) {
  const [lon, lat] = point;
  const rev = $api.useQuery("get", "/geocode/reverse", { params: { query: { lon, lat } } }, { staleTime: 300_000 });
  const address = rev.data?.find((h) => h.kind === "address");
  const district = rev.data?.find((h) => h.kind === "territory");
  const from = `${lon.toFixed(5)},${lat.toFixed(5)}`;
  return (
    <section className={`${s.float} ${s.here}`} aria-label="Что здесь">
      <header className={s.areaHead}>
        <h2>Что здесь</h2>
        <IconButton label="Закрыть" onPress={onClose}>
          <X size={16} />
        </IconButton>
      </header>
      {rev.isPending ? (
        <Skeleton height={40} />
      ) : (
        <dl className={s.areaFacts}>
          <dt>Адрес</dt>
          <dd>{address ? address.title : "в 50 м адресов нет"}</dd>
          {district && (
            <>
              <dt>Территория</dt>
              <dd>
                <Link to="/territories/$territoryId" params={{ territoryId: district.ref }}>
                  {district.title}
                </Link>
              </dd>
            </>
          )}
          <dt>Координаты</dt>
          <dd className="num">
            {num(lat, 5)}, {num(lon, 5)}
          </dd>
        </dl>
      )}
      <Link to="/network" search={{ from, name: address?.title }} className="btn btn-sm">
        <Route size={14} aria-hidden /> Маршрут отсюда
      </Link>
    </section>
  );
}
