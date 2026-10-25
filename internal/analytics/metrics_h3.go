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
