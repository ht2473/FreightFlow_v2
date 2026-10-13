import { Link } from "@tanstack/react-router";
import { AlertTriangle, ExternalLink, Map as MapIcon, Scale } from "lucide-react";
import { useMemo } from "react";
import { Guard } from "@/app/Guard";
import { $api, type Schemas } from "@/shared/api/client";
import { ago, date, num } from "@/shared/format";
import { Badge, cellClass, EmptyState, PageHeader, Section, Skeleton, tableClass } from "@/shared/ui";
import { Chart, type ChartSeries } from "@/shared/ui/Chart";
import { KpiTile } from "@/shared/ui/KpiTile";
import {
  conflictLabel,
  docRequisites,
  docShort,
  docStatus,
  effectLabel,
  monthLabel,
  monthRange,
  restrictionKind,
  ruleBasis,
  ruleRequirement,
  ruleSubject,
  zoneScope,
  zoneTitle,
} from "./labels";
import s from "./regulation.module.css";

type Overview = Schemas["RegulationOverview"];
type Rule = Schemas["ZoneRule"];
type Doc = Schemas["LegalDocument"];

const zones: Rule["zone"][] = ["sk", "ttk", "mkad"];

export function RegulationPage() {
  return (
    <Guard permission="map.view">
      <Regulation />
    </Guard>
  );
}

function Regulation() {
  const overview = $api.useQuery("get", "/regulation");
  const o = overview.data;
  return (
    <>
      <PageHeader
        title="Регулирование"
        subtitle="Зоны ограничения грузового движения, правила с правовыми основаниями, пропуска и спецразрешения"
      />
      {overview.isPending || !o ? (
        <Section>
          <Skeleton height={120} />
        </Section>
      ) : (
        <>
          <Notices o={o} />
          <Zones o={o} />
          <CargoFrame frame={o.cargoFrame} />
        </>
      )}
      <Signs />
      <Passes />
      <Permits />
      {o && <Documents docs={o.documents} pages={o.pages} />}
    </>
  );
}

function Notices({ o }: { o: Overview }) {
  const unconfirmed = o.rules.filter((r) => !r.confirmed).length;
  const changed = o.pages.filter((p) => p.changedSinceVerification);
  if (unconfirmed === 0 && changed.length === 0) {
    return null;
  }
  return (
    <div className={s.notices}>
      {changed.length > 0 && (
        <div className={`notice ${s.noticeWarn}`} role="status">
          <AlertTriangle size={16} aria-hidden />
          <span>
            Официальные страницы изменились после сверки правил: {changed.map((p) => p.title).join("; ")}. Правила нужно
            перепроверить.
          </span>
        </div>
      )}
      {unconfirmed > 0 && (
        <div className="notice notice-info" role="status">
          <AlertTriangle size={16} aria-hidden />
          <span>
            {unconfirmed}{" "}
            {plural(unconfirmed, "правило не подтверждено", "правила не подтверждены", "правил не подтверждены")}{" "}
            официальным источником — в расчётах они используются с предупреждением.
          </span>
        </div>
      )}
    </div>
  );
}

function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) {
    return one;
  }
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) {
    return few;
  }
  return many;
}

function Zones({ o }: { o: Overview }) {
  const docs = useMemo(() => new Map(o.documents.map((d) => [d.code, d])), [o.documents]);
  const ring = new Map(o.zones.map((z) => [z.code, z]));
  return (
    <Section
      title="Зоны и правила"
      description="Правило действует в пределах кольца и на самом кольце; более строгое правило внутренней зоны дополняет внешнее."
    >
      <div className={s.zones}>
        {zones.map((code) => {
          const z = ring.get(code);
          const rules = o.rules.filter((r) => r.zone === code);
          return (
            <div key={code} className={s.zone}>
              <div className={s.zoneHead}>
                <h3>{zoneTitle[code]}</h3>
                <div className={s.muted}>
                  {zoneScope[code]}
                  {z && (
                    <>
                      {" "}
                      · кольцо {num(z.lengthKm)} км{z.areaKm2 !== undefined && <>, зона {num(z.areaKm2)} км²</>}
                    </>
                  )}
                </div>
              </div>
              <ul className={s.rules}>
                {rules.map((r) => (
                  <RuleItem key={r.code} rule={r} docs={docs} />
                ))}
              </ul>
            </div>
          );
        })}
      </div>
    </Section>
  );
}

function RuleItem({ rule: r, docs }: { rule: Rule; docs: Map<string, Doc> }) {
  const eff = effectLabel[r.effect];
  const req = ruleRequirement(r);
  const basis = docs.get(r.document);
  const confirmedBy = r.confirmedBy ? docs.get(r.confirmedBy) : undefined;
  return (
    <li className={s.rule}>
      <div className={s.ruleHead}>
        <Badge tone={eff.tone}>{eff.label}</Badge>
        {r.confirmed ? (
          <Badge tone="ok" plain>
            подтверждено
          </Badge>
        ) : (
          <Badge tone="warn" plain>
            не подтверждено
          </Badge>
        )}
      </div>
      <div className={s.ruleTitle}>{r.title}</div>
      <div className={s.ruleMeta}>
        {ruleSubject(r)}
        {req && <> · {req}</>}
        {r.validFrom && <> · с {date(r.validFrom)}</>}
      </div>
      <div className={s.ruleMeta}>
        Основание:{" "}
        {basis?.url ? (
          <a href={basis.url} target="_blank" rel="noreferrer">
            {ruleBasis(r, docs)}
          </a>
        ) : (
          ruleBasis(r, docs)
        )}
        {confirmedBy && confirmedBy.code !== r.document && (
          <>
            {" "}
            · сверено:{" "}
            {confirmedBy.url ? (
              <a href={confirmedBy.url} target="_blank" rel="noreferrer" title={confirmedBy.title}>
                {docShort(confirmedBy)}
              </a>
            ) : (
              docShort(confirmedBy)
            )}
          </>
        )}
      </div>
      {r.note && <div className={s.ruleNote}>{r.note}</div>}
    </li>
  );
}

function CargoFrame({ frame }: { frame: Schemas["CargoFrame"] }) {
  const source = "Дептранс Москвы (transport.mos.ru), участки сети — OpenStreetMap";
  return (
    <Section
      title="Грузовой каркас"
      description="Улицы внутри МКАД, по которым разрешено движение грузовиков более 2,5 т; в жилых зонах вне каркаса — только для обслуживания района."
      actions={
        <Link to="/map" search={{ l: "rings,cargo_frame,restrictions", z: 11 }} className="btn btn-sm">
          <MapIcon size={14} aria-hidden /> На карте
        </Link>
      }
    >
      {frame.streets === 0 ? (
        <EmptyState title="Перечень улиц ещё не загружен">
          Загрузите источник «Грузовой каркас и правила для грузового транспорта» в разделе «Источники данных».
        </EmptyState>
      ) : (
        <>
          <div className={s.kpis}>
            <KpiTile label="Строк в перечне" value={frame.streets} source={source} />
            <KpiTile
              label="Найдено в дорожной сети"
              value={frame.streets ? Math.round((100 * frame.matched) / frame.streets) : null}
              unit="%"
              source={source}
              method="Улица перечня сопоставлена с участками OSM по названию в своём округе; кольца — по линии кольца. Строки с участком улицы отмечены целиком."
            />
            <KpiTile label="Протяжённость участков" value={frame.lengthKm} unit="км" source={source} />
          </div>
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Округ</th>
                <th className={cellClass.right}>Строк</th>
                <th className={cellClass.right}>Найдено</th>
                <th className={cellClass.right}>Участки, км</th>
              </tr>
            </thead>
            <tbody>
              {frame.okrugs.map((k) => (
                <tr key={k.okrug}>
                  <td>{k.okrug}</td>
                  <td className={`${cellClass.right} num`}>{num(k.streets)}</td>
                  <td className={`${cellClass.right} num`}>{num(k.matched)}</td>
                  <td className={`${cellClass.right} num`}>{num(k.lengthKm, 1)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className={s.muted}>
            Строк с участком улицы: {num(frame.partial)}; на карте они показаны пунктиром. Перечень обновлён{" "}
            {ago(frame.updatedAt)}.
          </p>
        </>
      )}
    </Section>
  );
}

function Signs() {
  const q = $api.useQuery("get", "/regulation/signs");
  const d = q.data;
  const source = "реестр дорожных знаков Москвы (data.mos.ru), OpenStreetMap";
  return (
    <Section
      title="Знаки и ограничения"
      description={`Знаки ограничений для грузового движения из реестра Москвы: привязка к участкам сети и расхождения с OSM${
        d?.updatedAt ? `; загружено ${ago(d.updatedAt)}` : ""
      }.`}
      actions={
        <Link to="/map" search={{ l: "restrictions,restriction_conflicts", z: 12 }} className="btn btn-sm">
          <MapIcon size={14} aria-hidden /> На карте
        </Link>
      }
    >
      {q.isPending ? (
        <Skeleton height={200} />
      ) : !d || d.restrictionSigns === 0 ? (
        <EmptyState title="Знаки ещё не загружены" />
      ) : (
        <>
          <div className={s.kpis}>
            <KpiTile label="Знаков ограничений" value={d.restrictionSigns} source={source} />
            <KpiTile
              label="Привязано к участкам"
              value={Math.round((100 * (d.matched + d.ambiguous)) / d.restrictionSigns)}
              unit="%"
              source={source}
              method="Знак привязан к ближайшему участку в 25 м; неоднозначно — рядом участок другой улицы почти на том же расстоянии."
            />
            <KpiTile label="Ограничений из знаков" value={d.restrictions} source={source} />
          </div>
          <div className={s.charts}>
            <div>
              <h3 className={s.chartTitle}>Виды знаков</h3>
              <table className={tableClass}>
                <thead>
                  <tr>
                    <th>Знак</th>
                    <th className={cellClass.right}>Всего</th>
                    <th className={cellClass.right}>Привязано</th>
                    <th className={cellClass.right}>Неоднозначно</th>
                    <th className={cellClass.right}>Нет</th>
                  </tr>
                </thead>
                <tbody>
                  {d.types.map((t) => (
                    <tr key={t.code}>
                      <td>
                        <span className="num">{t.code}</span> {t.name}
                      </td>
                      <td className={`${cellClass.right} num`}>{num(t.count)}</td>
                      <td className={`${cellClass.right} num`}>{num(t.matched)}</td>
                      <td className={`${cellClass.right} num`}>{num(t.ambiguous)}</td>
                      <td className={`${cellClass.right} num`}>{num(t.unmatched)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div>
              <h3 className={s.chartTitle}>Расхождения с OSM, участков</h3>
              <table className={tableClass}>
                <thead>
                  <tr>
                    <th>Расхождение</th>
                    <th>Ограничение</th>
                    <th className={cellClass.right}>Участков</th>
                  </tr>
                </thead>
                <tbody>
                  {d.conflicts.map((c) => (
                    <tr key={`${c.type}-${c.kind}`}>
                      <td>{conflictLabel[c.type]}</td>
                      <td>{restrictionKind[c.kind] ?? c.kind}</td>
                      <td className={`${cellClass.right} num`}>{num(c.segments)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className={s.muted}>
                Знак подтверждает ограничение, а его значение (масса, высота) берётся из OSM: в реестре знаков значений
                нет.
              </p>
            </div>
          </div>
        </>
      )}
    </Section>
  );
}

const zoneColor: Record<string, string> = {};
const palette = ["cat1", "cat2", "cat3", "cat4", "cat5", "cat6"];

function colorFor(zone: string): string {
  zoneColor[zone] ??= palette[Object.keys(zoneColor).length % palette.length] ?? "cat8";
  return zoneColor[zone];
}

function Passes() {
  const q = $api.useQuery("get", "/regulation/passes");
  const charts = useMemo(() => {
    const series = q.data?.series ?? [];
    if (series.length === 0) {
      return null;
    }
    const seen = [...new Set(series.flatMap((x) => x.points.map((p) => p.month)))].sort();
    const months = monthRange(seen[0] ?? "", seen[seen.length - 1] ?? "");
    const idx = new Map(months.map((m, i) => [m, i]));
    const sum = (filter: (x: Schemas["PassSeries"]) => boolean) => {
      const out = months.map(() => 0);
      for (const x of series.filter(filter)) {
        for (const p of x.points) {
          const i = idx.get(p.month);
          if (i !== undefined) {
            out[i] = (out[i] ?? 0) + p.value;
          }
        }
      }
      return out;
    };
    const zonesSeen = [...new Set(series.map((x) => x.zone))].sort();
    const valid: ChartSeries[] = zonesSeen.map((z) => ({
      name: z,
      kind: "line",
      color: colorFor(z),
      data: sum((x) => x.metric === "valid" && x.zone === z),
    }));
    const flow: ChartSeries[] = [
      { name: "Начали действовать", kind: "bar", color: "cat1", data: sum((x) => x.metric === "issued") },
      { name: "Аннулированы", kind: "bar", color: "cat2", data: sum((x) => x.metric === "cancelled") },
    ];
    return { labels: months.map(monthLabel), valid, flow };
  }, [q.data]);

  return (
    <Section
      title="Пропуска грузового транспорта"
      description={`Агрегаты реестра пропусков по месяцам без номеров и сведений о ТС${
        q.data?.updatedAt ? `; загружено ${ago(q.data.updatedAt)}` : ""
      }.`}
    >
      {q.isPending ? (
        <Skeleton height={260} />
      ) : !charts ? (
        <EmptyState icon={<Scale size={20} strokeWidth={1.7} />} title="Пропуска ещё не загружены">
          Набор реестра пропусков (около миллиона записей) загружается по расписанию раз в неделю.
        </EmptyState>
      ) : (
        <div className={s.charts}>
          <div>
            <h3 className={s.chartTitle}>Действующие на первое число месяца, по зонам</h3>
            <Chart labels={charts.labels} series={charts.valid} label="Действующие пропуска по зонам и месяцам" />
          </div>
          <div>
            <h3 className={s.chartTitle}>Начали действовать и аннулированы за месяц</h3>
            <Chart labels={charts.labels} series={charts.flow} label="Выданные и аннулированные пропуска по месяцам" />
          </div>
        </div>
      )}
    </Section>
  );
}

function Permits() {
  const q = $api.useQuery("get", "/regulation/permits");
  const d = q.data;
  const chart = useMemo(() => {
    const first = d?.months[0];
    const last = d?.months[d.months.length - 1];
    if (!d || !first || !last) {
      return null;
    }
    const byMonth = new Map(d.months.map((m) => [m.month, m.value]));
    const months = monthRange(first.month, last.month);
    return {
      first: first.month,
      last: last.month,
      labels: months.map(monthLabel),
      series: [
        { name: "Выдано", kind: "bar", color: "acc", data: months.map((m) => byMonth.get(m) ?? 0) },
      ] as ChartSeries[],
    };
  }, [d]);
  return (
    <Section
      title="Спецразрешения на тяжеловесные и крупногабаритные ТС"
      description={`Без сведений о перевозчике и ТС${
        chart ? `; выданы с ${monthLabel(chart.first)} по ${monthLabel(chart.last)}` : ""
      }${d?.updatedAt ? `; загружено ${ago(d.updatedAt)}` : ""}.`}
    >
      {q.isPending ? (
        <Skeleton height={200} />
      ) : !d || !chart ? (
        <EmptyState title="Спецразрешения ещё не загружены" />
      ) : (
        <div className={s.charts}>
          <div>
            <h3 className={s.chartTitle}>Выдано за месяц</h3>
            <Chart labels={chart.labels} series={chart.series} label="Выданные спецразрешения по месяцам" />
          </div>
          <div>
            <h3 className={s.chartTitle}>Превышенные параметры</h3>
            <table className={tableClass}>
              <tbody>
                {d.exceeded.map((t) => (
                  <tr key={t.name}>
                    <td>{t.name}</td>
                    <td className={`${cellClass.right} num`}>{num(t.count)}</td>
                    <td className={`${cellClass.right} num`}>{num(d.total ? (100 * t.count) / d.total : null)} %</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className={s.muted}>
              Всего разрешений: {num(d.total)}; одно разрешение может превышать несколько параметров. Допустимая масса
              зависит от числа осей — разрез по массе ориентировочный.
            </p>
          </div>
        </div>
      )}
    </Section>
  );
}

function Documents({ docs, pages }: { docs: Doc[]; pages: Schemas["OfficialPage"][] }) {
  const page = new Map(pages.map((p) => [p.code, p]));
  return (
    <Section title="Правовые основания и официальные страницы" flush>
      <table className={tableClass}>
        <thead>
          <tr>
            <th>Документ</th>
            <th>Орган</th>
            <th>Статус</th>
            <th>Сверка</th>
          </tr>
        </thead>
        <tbody>
          {docs.map((d) => {
            const st = docStatus[d.status];
            const p = page.get(d.code);
            return (
              <tr key={d.code}>
                <td>
                  <div className={s.docTitle}>
                    {d.url ? (
                      <a href={d.url} target="_blank" rel="noreferrer">
                        {d.title} <ExternalLink size={12} aria-label="(открывается в новой вкладке)" />
                      </a>
                    ) : (
                      d.title
                    )}
                  </div>
                  <div className={s.muted}>
                    {docRequisites(d)}
                    {d.effectiveOn && <> · вступает в силу {date(d.effectiveOn)}</>}
                    {d.amends && <> · изменяет {docs.find((x) => x.code === d.amends)?.number}</>}
                  </div>
                  {d.summary && <div className={s.docSummary}>{d.summary}</div>}
                </td>
                <td className={s.muted}>{d.authority}</td>
                <td>
                  <Badge tone={st.tone}>{st.label}</Badge>
                </td>
                <td>
                  {d.verifiedOn ? date(d.verifiedOn) : "—"}
                  {p && (
                    <div className={s.muted}>
                      проверено {ago(p.fetchedAt)}
                      {p.changedSinceVerification && (
                        <div>
                          <Badge tone="warn">изменилась</Badge>
                        </div>
                      )}
                    </div>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Section>
  );
}
