import { Link } from "@tanstack/react-router";
import { Map as MapIcon, Network, Route } from "lucide-react";
import { Guard } from "@/app/Guard";
import { $api, isProblem, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime, num } from "@/shared/format";
import { cellClass, EmptyState, PageHeader, Section, Skeleton, tableClass } from "@/shared/ui";
import { KpiTile } from "@/shared/ui/KpiTile";
import s from "./analysis.module.css";
import { accuracyText, damageText, detourText, elementLabel, limitText, mapCenter, segmentsLabel } from "./labels";
import { ResilienceSection } from "./ResilienceSection";

type Analysis = Schemas["NetworkAnalysis"];

const source = "OpenStreetMap, знаки Москвы; расчёт по графу сети";
const centralityLayers = "edge_centrality,rings";
const bottleneckLayers = "bottlenecks,restrictions";
const criticalLayers = "critical_elements,rings";

export function AnalysisPage() {
  return (
    <Guard permission="analytics.view">
      <NetworkAnalysis />
    </Guard>
  );
}

function NetworkAnalysis() {
  const q = $api.useQuery("get", "/routing/analysis", { params: { query: { top: 20 } } });
  return (
    <>
      <PageHeader
        title="Анализ сети"
        subtitle="Значимость участков для грузовика 12 т и узкие места по массе и габаритам"
        actions={
          <>
            <Link to="/network" className="btn btn-sm">
              <Route size={14} aria-hidden /> Маршруты и изохроны
            </Link>
            <Link to="/map" search={{ l: `${centralityLayers},critical_elements`, z: 11 }} className="btn btn-sm">
              <MapIcon size={14} aria-hidden /> На карте
            </Link>
          </>
        }
      />
      {q.isPending ? (
        <Section>
          <Skeleton height={120} />
        </Section>
      ) : q.error ? (
        <Section>
          {isProblem(q.error) && q.error.code === "no_analysis" ? (
            <EmptyState icon={<Network size={20} />} title="Сетевой анализ ещё не выполнен">
              Граф строится фоновой задачей после загрузки дорожной сети; вручную — командой freightflow routing
              analyze.
            </EmptyState>
          ) : (
            <div className="notice notice-error" role="alert">
              {problemMessage(q.error)}
            </div>
          )}
        </Section>
      ) : (
        <>
          <Overview a={q.data} />
          <Streets a={q.data} />
          <Critical a={q.data} />
          <ResilienceSection />
          <Bottlenecks a={q.data} />
          <Method a={q.data} />
        </>
      )}
    </>
  );
}

function Overview({ a }: { a: Analysis }) {
  return (
    <Section
      title="Граф анализа"
      description={`Профиль «${a.profile}», построен ${dateTime(a.builtAt)} за ${num(a.durationSeconds)} с`}
    >
      <div className={s.kpis}>
        <KpiTile label="Узлов" value={a.nodes} source={source} method="Перекрёстки и концы линий дорожной сети." />
        <KpiTile label="Участков" value={a.edges} source={source} />
        <KpiTile
          label="Связность для профиля"
          value={a.largestComponent * 100}
          unit="%"
          fractionDigits={1}
          source={source}
          method="Доля узлов в наибольшей сильно связной компоненте: из любого такого узла грузовик 12 т может доехать до любого другого с учётом одностороннего движения, запретов и пределов массы и габаритов."
        />
        <KpiTile
          label="Узких мест по габаритам"
          value={a.bottleneckCount}
          source={source}
          method={`Участки, закрытые грузовику 12 т пределом массы или габарита, через которые без предела шли бы кратчайшие пути. Без объезда — ${num(a.noDetourCount)}.`}
        />
      </div>
      <p className={s.accuracy}>{accuracyText(a.accuracy, a.nodes)}</p>
    </Section>
  );
}

function Streets({ a }: { a: Analysis }) {
  return (
    <Section
      title="Улицы с наибольшей значимостью"
      description="Доля кратчайших по времени путей между всеми парами узлов сети, проходящих через участок улицы"
      actions={
        <Link to="/map" search={{ l: centralityLayers, z: 11 }} className="btn btn-sm">
          <MapIcon size={14} aria-hidden /> Слой значимости
        </Link>
      }
      flush
    >
      {a.topStreets.length === 0 ? (
        <EmptyState title="Значимых участков нет" />
      ) : (
        <table className={tableClass}>
          <thead>
            <tr>
              <th className={cellClass.right}>Место</th>
              <th>Улица</th>
              <th className={cellClass.right}>Доля путей, %</th>
              <th className={cellClass.right}>В верхних 5 %, км</th>
              <th aria-label="Карта" />
            </tr>
          </thead>
          <tbody>
            {a.topStreets.map((r) => (
              <tr key={r.name}>
                <td className={`${cellClass.right} num`}>{num(r.rank)}</td>
                <td>{r.name}</td>
                <td className={`${cellClass.right} num`}>{num(r.share, 2)}</td>
                <td className={`${cellClass.right} num`}>{num(r.topKm, 1)}</td>
                <td className={cellClass.right}>
                  <Link
                    to="/map"
                    search={{ c: mapCenter(r.point), z: 15, l: centralityLayers }}
                    aria-label={`${r.name} на карте`}
                    className={s.mapLink}
                  >
                    <MapIcon size={14} aria-hidden />
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function Critical({ a }: { a: Analysis }) {
  const c = a.criticality;
  return (
    <Section
      title="Критичные мосты и участки"
      description={`Рост времени доступа объектов реестра к ближайшей точке МКАД при закрытии элемента. Оценено ${num(c.elements)} элементов (мостов — ${num(c.bridges)}); объектов привязано к графу ${num(c.facilitiesSnapped)} из ${num(c.facilities)}; первые ${num(c.confirmed)} проверены матрицами службы маршрутизации.`}
      actions={
        <Link to="/map" search={{ l: criticalLayers, z: 11 }} className="btn btn-sm">
          <MapIcon size={14} aria-hidden /> Слой критичности
        </Link>
      }
      flush
    >
      {a.critical.length === 0 ? (
        <EmptyState title="Критичных элементов нет">
          Закрытие ни одного моста или значимого участка не увеличивает время доступа объектов к МКАД.
        </EmptyState>
      ) : (
        <table className={tableClass}>
          <thead>
            <tr>
              <th className={cellClass.right}>Место</th>
              <th>Элемент</th>
              <th className={cellClass.right}>Объектов</th>
              <th className={cellClass.right}>Рост, объекто-мин</th>
              <th className={cellClass.right}>Наибольший, мин</th>
              <th className={cellClass.right}>Без доступа</th>
              <th>Проверка матрицами</th>
              <th aria-label="Карта" />
            </tr>
          </thead>
          <tbody>
            {a.critical.map((el) => (
              <tr key={el.id}>
                <td className={`${cellClass.right} num`}>{num(el.rank)}</td>
                <td>
                  {el.name || "Без названия"}
                  <div className={s.kind}>{elementLabel(el.kind, el.same)}</div>
                </td>
                <td className={`${cellClass.right} num`}>{num(el.graph.facilities)}</td>
                <td className={`${cellClass.right} num`}>{num(el.graph.minutes)}</td>
                <td className={`${cellClass.right} num`}>{num(el.graph.maxDelay, 1)}</td>
                <td className={`${cellClass.right} num`}>{num(el.graph.disconnected)}</td>
                <td>{el.valhalla ? damageText(el.valhalla) : <span className={s.kind}>не проверялся</span>}</td>
                <td className={cellClass.right}>
                  <Link
                    to="/map"
                    search={{ c: mapCenter(el.point), z: 15, l: criticalLayers }}
                    aria-label={`${el.name || "Элемент"} на карте`}
                    className={s.mapLink}
                  >
                    <MapIcon size={14} aria-hidden />
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function Bottlenecks({ a }: { a: Analysis }) {
  return (
    <Section
      title="Узкие места по габаритам"
      description="По значимости путей, которые шли бы через участок без ограничения; объезд — дополнительное время в обход по сети"
      actions={
        <Link to="/map" search={{ l: bottleneckLayers, z: 11 }} className="btn btn-sm">
          <MapIcon size={14} aria-hidden /> Слой узких мест
        </Link>
      }
      flush
    >
      {a.bottlenecks.length === 0 ? (
        <EmptyState title="Узких мест нет">Ограничения массы и габаритов не закрывают значимых путей.</EmptyState>
      ) : (
        <table className={tableClass}>
          <thead>
            <tr>
              <th>Улица</th>
              <th>Ограничение</th>
              <th className={cellClass.right}>Место без ограничения</th>
              <th className={cellClass.right}>Доля путей, %</th>
              <th className={cellClass.right}>Объезд</th>
              <th aria-label="Карта" />
            </tr>
          </thead>
          <tbody>
            {a.bottlenecks.map((b) => (
              <tr key={b.segmentId}>
                <td>
                  {b.name || "Без названия"}
                  {b.segments > 1 && <div className={s.kind}>{segmentsLabel(b.segments)}</div>}
                </td>
                <td>{limitText(b.kind, b.value)}</td>
                <td className={`${cellClass.right} num`}>{num(b.freeRank)}</td>
                <td className={`${cellClass.right} num`}>{num(b.freeShare, 2)}</td>
                <td className={`${cellClass.right} num`}>{detourText(b.detourMinutes)}</td>
                <td className={cellClass.right}>
                  <Link
                    to="/map"
                    search={{ c: mapCenter(b.point), z: 16, l: bottleneckLayers }}
                    aria-label={`${b.name || "Участок"} на карте`}
                    className={s.mapLink}
                  >
                    <MapIcon size={14} aria-hidden />
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function Method({ a }: { a: Analysis }) {
  return (
    <Section title="Методика">
      <ul className={s.method}>
        <li>
          Граф — все дороги, кроме грунтовых (проезды — без парковочных и дворовых): узлы — общие точки линий OSM, у
          мостов и тоннелей над дорогами общих точек нет. Время проезда — по ограничению скорости или классу дороги с
          пределами для грузовиков (90 км/ч на автомагистралях, 70 — на остальных).
        </li>
        <li>
          Проезд закрыт запретом движения грузовиков и пределом массы, нагрузки на ось или габарита меньше параметров
          ТС; условия по времени не закрывают проезд, участки «только для подъезда» допускаются с пятикратным временем.
          Зоны пропусков не учитываются — это физическая проходимость сети.
        </li>
        <li>
          Критичность — для всех мостов (проезжие части одного сооружения вместе) и {num(100)} самых значимых участков:
          время от каждого объекта Москвы (привязка к местной улице в 500 м) до ближайшей точки МКАД по графу без рёбер
          элемента против базового. Ущерб — суммарный рост времени, объект без доступа считается как 60 мин. Первые{" "}
          {num(30)} элементов по графу пересчитываются матрицами службы маршрутизации с исключением участков элемента,
          их места — по результату пересчёта.
        </li>
        <li>
          Узкое место — участок, закрытый пределом массы или габарита, через который без предела шли бы кратчайшие пути.
          Объезд — разница времени между концами закрытого отрезка по сети для 12 т и без пределов; «нет объезда» — за
          ограничением участки, куда иначе не проехать.
        </li>
        <li>
          Значимость — выборочный алгоритм Брандеса: кратчайшие пути от случайных узлов-источников порциями до заданной
          точности. {accuracyText(a.freeAccuracy, a.nodes, "Без пределов габаритов")}
        </li>
      </ul>
    </Section>
  );
}
