package analytics

// cells — ячейки H3 обоих разрешений с уровнем и идентификатором.
const cells = `
	WITH c AS (
		SELECT 'h3_' || res AS level, h3::text AS entity, h3, res, district_id, okrug_id, geom, area_km2
		FROM territory.h3_cell
	)`

var h3Levels = []string{LevelH3r8, LevelH3r9}

// h3Count — число точек таблицы в ячейке (по ячейке точки того же разрешения).
func h3Count(code, title, unit, from, point, where, method string, domains []string, daily bool) Metric {
	return Metric{
		Code: code, Title: title, Unit: unit, Levels: h3Levels, Version: 1, Daily: daily,
		Formula: "число в ячейке H3", Method: method, Domains: domains,
		Compute: cells + `,
		x AS (SELECT h3_lat_lng_to_cell(` + point + `, r.res) AS h3 FROM ` + from + `, (VALUES (8), (9)) r (res) WHERE ` + where + `)
		SELECT c.level, c.entity, NULL::date, count(x.h3)::float8, '{}'::jsonb
		FROM c LEFT JOIN x ON x.h3 = c.h3
		GROUP BY c.level, c.entity`,
	}
}

var h3Metrics = []Metric{
	h3Count("h3.facilities", "Объектов логистики в ячейке", "объектов", "infra.facility f", "f.point", "f.status = 'active'",
		"Действующие объекты реестра, точка которых лежит в ячейке.", []string{"facility", "territory"}, false),
	h3Count("h3.infra", "Инфраструктура для грузовиков в ячейке", "точек", "infra.poi p", "p.point",
		"p.kind IN ('fuel', 'truck_parking', 'rest_area')",
		"АЗС, стоянки грузового транспорта и площадки отдыха (реестры Москвы и OSM) в ячейке.", []string{"infra", "territory"}, false),
	h3Count("h3.works", "Дорожные работы в ячейке", "работ", "operations.road_work w", "ST_PointOnSurface(w.geom)",
		"w.state = 'active' AND w.removed_at IS NULL AND w.geom IS NOT NULL",
		"Дорожные работы, действующие на дату расчёта; работа — в ячейке точки на её геометрии.", []string{"operations", "territory"}, true),
	{
		Code: "h3.building_area", Title: "Складские площади в ячейке", Unit: "м²", Levels: h3Levels, Version: 1,
		Formula: "Σ площади застройки объектов реестра в ячейке",
		Method:  "Площадь застройки по контурам зданий объектов реестра (OSM); объект — в ячейке своей точки.",
		Domains: []string{"facility", "territory"},
		Compute: cells + `,
		x AS (SELECT h3_lat_lng_to_cell(f.point, r.res) AS h3, f.building_area_m2 AS m2
		      FROM infra.facility f, (VALUES (8), (9)) r (res) WHERE f.status = 'active')
		SELECT c.level, c.entity, NULL::date, coalesce(sum(x.m2), 0)::float8, '{}'::jsonb
		FROM c LEFT JOIN x ON x.h3 = c.h3
		GROUP BY c.level, c.entity`,
	},
	{
		Code: "h3.mkad_min", Title: "Время до МКАД объектов ячейки", Unit: "мин", Levels: h3Levels, Version: 1,
		Formula:   "медиана времени до МКАД объектов ячейки",
		Method:    "Медиана показателя «Время до МКАД» по объектам реестра в ячейке; ячейки без объектов значения не имеют.",
		DependsOn: []string{"facility.mkad_min"},
		Domains:   []string{"territory"},
		Compute: cells + `,
		x AS (SELECT h3_lat_lng_to_cell(f.point, r.res) AS h3, v.value
		      FROM infra.facility f
		      JOIN analytics.metric_value v ON v.metric_code = 'facility.mkad_min' AND v.level = 'facility' AND v.entity = f.id::text,
		      (VALUES (8), (9)) r (res)
		      WHERE f.status = 'active')
		SELECT c.level, c.entity, NULL::date, percentile_cont(0.5) WITHIN GROUP (ORDER BY x.value)::float8,
		       jsonb_build_object('facilities', count(*))
		FROM c JOIN x ON x.h3 = c.h3
		GROUP BY c.level, c.entity`,
	},
	{
		Code: "h3.restricted_km", Title: "Сеть с ограничениями в ячейке", Unit: "км", Levels: h3Levels, Version: 1,
		Formula: "Σ длины участков улично-дорожной сети с запретом грузовиков или пределом массы и габаритов",
		Method:  "Как «Сеть с ограничениями для грузовиков» территорий; участок — в ячейке своей середины.",
		Domains: []string{"network", "territory"},
		Compute: cells + `,
		x AS (SELECT h3_lat_lng_to_cell(ST_LineInterpolatePoint(s.geom, 0.5), r.res) AS h3, s.length_m
		      FROM network.road_segment s, (VALUES (8), (9)) r (res)
		      WHERE s.class IN (` + streetClasses + `)
		        AND EXISTS (SELECT 1 FROM network.restriction q WHERE q.segment_id = s.id AND q.kind IN (` + restrictionKinds + `)))
		SELECT c.level, c.entity, NULL::date, coalesce(sum(x.length_m), 0)::float8 / 1000, '{}'::jsonb
		FROM c LEFT JOIN x ON x.h3 = c.h3
		GROUP BY c.level, c.entity`,
	},
	{
		Code: "h3.population", Title: "Население ячейки (модель)", Unit: "чел.", Levels: h3Levels, Version: 1,
		Formula: "население района × здания ячейки / здания района",
		Method: "Модельная оценка: население района (Росстат) распределяется по ячейкам пропорционально числу зданий " +
			"адресного реестра Москвы (вид «здание») с центром в ячейке. Жилые и нежилые здания не различаются: " +
			"в промзонах и деловых кварталах оценка завышена, в многоэтажной застройке — занижена.",
		Domains:   []string{"territory"},
		DependsOn: []string{"territory.population"},
		Compute: cells + `,
		b AS (SELECT h3_lat_lng_to_cell(a.point, r.res) AS h3, r.res, count(*) AS n
		      FROM territory.address a, (VALUES (8), (9)) r (res)
		      WHERE a.obj_type = 'здание' AND a.point IS NOT NULL GROUP BY 1, 2),
		cb AS (SELECT c.*, coalesce(b.n, 0) AS n FROM c LEFT JOIN b ON b.h3 = c.h3),
		db AS (SELECT district_id, res, sum(n) AS n FROM cb WHERE district_id IS NOT NULL GROUP BY 1, 2)
		SELECT cb.level, cb.entity, NULL::date, (p.value * cb.n / db.n)::float8,
		       jsonb_build_object('buildings', cb.n, 'district_buildings', db.n)
		FROM cb
		JOIN db ON db.district_id = cb.district_id AND db.res = cb.res AND db.n > 0
		JOIN analytics.metric_value p ON p.metric_code = 'territory.population' AND p.level = 'district'
		     AND p.entity = cb.district_id::text`,
	},
	{
		Code: "territory.population_coverage", Title: "Покрытие населения объектами (3 км)", Unit: "%",
		Levels: territoryLevels, Version: 1,
		Formula: "население ячеек H3 (разрешение 9), центр которых в 3 км от объекта логистики / население территории × 100",
		Method: "Доля населения (модель «Население ячейки») в 3 км по прямой от ближайшего объекта реестра — " +
			"оценка доступности последней мили. Расстояние по прямой — нижняя граница пути по сети.",
		Domains:   []string{"facility"},
		DependsOn: []string{"h3.population"},
		Compute: areas + `,
		cp AS MATERIALIZED (SELECT h.district_id, h.okrug_id, p.value,
		              EXISTS (SELECT 1 FROM infra.facility f WHERE f.status = 'active'
		                      AND f.point && ST_Expand(h3_cell_to_geometry(h.h3), 0.05)
		                      AND ST_DWithin(f.point::geography, h3_cell_to_geometry(h.h3)::geography, 3000)) AS covered
		       FROM territory.h3_cell h
		       JOIN analytics.metric_value p ON p.metric_code = 'h3.population' AND p.level = 'h3_9' AND p.entity = h.h3::text
		       WHERE h.res = 9)
		SELECT a.level, a.entity, NULL::date,
		       (sum(cp.value) FILTER (WHERE cp.covered) / nullif(sum(cp.value), 0) * 100)::float8,
		       jsonb_build_object('covered', round(coalesce(sum(cp.value) FILTER (WHERE cp.covered), 0)::numeric),
		                          'population', round(sum(cp.value)::numeric))
		FROM a JOIN cp ON a.kind = 'district' AND cp.district_id = a.id OR a.kind = 'okrug' AND cp.okrug_id = a.id OR a.kind = 'region'
		GROUP BY a.level, a.entity`,
	},
}

// parentJoin — ячейка разрешения 8 для значения: сама ячейка или родитель ячейки разрешения 9.
const parentJoin = `CASE WHEN c.res = 8 THEN c.h3 ELSE h3_cell_to_parent(c.h3, 8) END`

// h3Access — время от центра ячейки до ближайшей цели вида (доступность ячеек, разрешение 8).
func h3Access(code, kind, title string) Metric {
	return Metric{
		Code: code, Title: title, Unit: "мин", Levels: h3Levels, Version: 1,
		Formula: "время грузовика 12 т от центра ячейки H3 разрешения 8 до ближайшей цели по графу маршрутизации",
		Method: "Матрицы службы маршрутизации (Valhalla) от центров ячеек разрешения 8 до ближайшей точки цели; " +
			"центр ячейки привязывается к сети в 300 м. Ячейки разрешения 9 берут значение родительской ячейки. " +
			"Правила зон пропусков не учитываются — физическая доступность по сети с ограничениями участков.",
		Domains: []string{"cell_access", "territory"},
		Compute: cells + `
			SELECT c.level, c.entity, NULL::date, a.minutes::float8, jsonb_build_object('km', round(a.km::numeric, 1))
			FROM c JOIN analytics.cell_access a ON a.h3 = ` + parentJoin + ` AND a.target_kind = '` + kind + `'
			WHERE a.minutes IS NOT NULL`,
	}
}

// h3Share — доля ячейки, покрытая слоем: по центрам дочерних ячеек разрешения 10 (49 на ячейку
// разрешения 8, 7 — разрешения 9), процентов.
func h3Share(code, title, from, cond, method string) Metric {
	return Metric{
		Code: code, Title: title, Unit: "%", Levels: h3Levels, Version: 1,
		Formula: "доля центров дочерних ячеек H3 разрешения 10 внутри слоя × 100",
		Method:  method + " Доля оценивается по центрам дочерних ячеек разрешения 10 (≈ 0,015 км²).",
		Domains: []string{"territory", "infra"},
		Compute: cells + `,
			ch AS (SELECT c9.h3, h3_cell_to_geometry(x) AS pt
			       FROM territory.h3_cell c9 CROSS JOIN LATERAL h3_cell_to_children(c9.h3, 10) x WHERE c9.res = 9),
			s9 AS (SELECT ch.h3, avg((EXISTS (SELECT 1 FROM ` + from + ` l
			                                  WHERE l.geom && ch.pt AND ST_Intersects(l.geom, ch.pt)` + cond + `))::int) AS share
			       FROM ch GROUP BY ch.h3),
			s8 AS (SELECT h3_cell_to_parent(h3, 8) AS h3, avg(share) AS share FROM s9 GROUP BY 1)
			SELECT c.level, c.entity, NULL::date,
			       round(100 * coalesce(CASE WHEN c.res = 9 THEN s9.share ELSE s8.share END, 0), 1)::float8, '{}'::jsonb
			FROM c LEFT JOIN s9 ON c.res = 9 AND s9.h3 = c.h3 LEFT JOIN s8 ON c.res = 8 AND s8.h3 = c.h3`,
	}
}

// Критерии оценки локации: доступность ячеек, спрос (население в радиусе), землепользование.
var siteMetrics = []Metric{
	h3Access("h3.access_mkad_min", "mkad", "Время от ячейки до МКАД"),
	h3Access("h3.access_ttk_min", "ttk", "Время от ячейки до ТТК"),
	h3Access("h3.access_rail_min", "rail_freight", "Время от ячейки до ж/д грузовых пунктов"),
	{
		Code: "h3.population_5km", Title: "Население в 5 км (модель)", Unit: "чел.", Levels: h3Levels, Version: 1,
		Formula: "Σ населения ячеек разрешения 8, центр которых в 5 км по прямой от центра ячейки",
		Method: "Спрос последней мили: модельное население ячеек H3 разрешения 8 (показатель «Население ячейки») " +
			"в 5 км по прямой. Ячейки разрешения 9 берут значение родительской ячейки.",
		Domains:   []string{"territory"},
		DependsOn: []string{"h3.population"},
		Compute: cells + `,
			p AS (SELECT entity::h3index AS h3, value FROM analytics.metric_value
			      WHERE metric_code = 'h3.population' AND level = 'h3_8' AND period IS NULL),
			r AS MATERIALIZED (
			    SELECT c8.h3, sum(p.value) AS pop
			    FROM territory.h3_cell c8
			    CROSS JOIN LATERAL h3_grid_disk(c8.h3, 6) AS d (h3)
			    JOIN p ON p.h3 = d.h3
			    WHERE c8.res = 8
			      AND ST_DWithin(h3_cell_to_geometry(c8.h3)::geography, h3_cell_to_geometry(d.h3)::geography, 5000)
			    GROUP BY c8.h3)
			SELECT c.level, c.entity, NULL::date, round(coalesce(r.pop, 0))::float8, '{}'::jsonb
			FROM c LEFT JOIN r ON r.h3 = ` + parentJoin,
	},
	h3Share("h3.share_industrial", "Доля промзон в ячейке", "infra.industrial_zone", "",
		"Промышленные и производственные территории OSM (landuse=industrial) — совместимое землепользование."),
	h3Share("h3.share_residential", "Доля жилой застройки в ячейке", "territory.land_cover", " AND l.kind = 'residential'",
		"Жилая застройка OSM (landuse=residential) — исключающая зона оценки локации."),
	h3Share("h3.share_green", "Доля парков и природных территорий в ячейке", "territory.land_cover", " AND l.kind = 'green'",
		"Парки, заповедники и охраняемые природные территории OSM."),
	h3Share("h3.share_water", "Доля водных объектов в ячейке", "territory.land_cover", " AND l.kind = 'water'",
		"Реки, пруды и водохранилища OSM."),
	h3Share("h3.share_heritage", "Доля зон охраны ОКН в ячейке", "territory.constraint_zone",
		" AND l.kind IN ('heritage_protection', 'heritage_territory')",
		"Зоны охраны и территории объектов культурного наследия (портал открытых данных Москвы)."),
	h3Share("h3.share_parks", "Доля парковых территорий Москвы в ячейке", "territory.constraint_zone", " AND l.kind = 'park'",
		"Парковые территории (портал открытых данных Москвы)."),
}
