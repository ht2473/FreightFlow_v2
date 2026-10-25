package analytics

// areas — границы уровней: районы, округа и Москва целиком (entity «moscow»).
const areas = `
	WITH a AS (
		SELECT CASE t.kind WHEN 'region' THEN 'city' ELSE t.kind END AS level,
		       CASE t.kind WHEN 'region' THEN 'moscow' ELSE t.id::text END AS entity,
		       t.id, t.kind, t.geom, t.area_km2
		FROM territory.admin_area t
		WHERE t.kind IN ('district', 'okrug') OR t.kind = 'region' AND t.name = 'Москва'
	)`

// facilityIn — объекты территории: по привязке реестра к району и округу, для города — все.
const facilityIn = `(a.kind = 'district' AND f.district_id = a.id OR a.kind = 'okrug' AND f.okrug_id = a.id OR a.kind = 'region')`

// midpointIn — участок сети относится к территории, где лежит его середина.
const midpointIn = `s.geom && a.geom AND ST_Intersects(a.geom, ST_LineInterpolatePoint(s.geom, 0.5))`

// streetClasses — улично-дорожная сеть без служебных проездов и полевых дорог.
const streetClasses = `'motorway', 'trunk', 'primary', 'secondary', 'tertiary', 'unclassified', 'residential', 'living_street'`

// mainClasses — магистральная сеть, по которой ходят грузовики между районами.
const mainClasses = `'motorway', 'trunk', 'primary', 'secondary', 'tertiary'`

var territoryLevels = []string{LevelDistrict, LevelOkrug, LevelCity}

var territoryMetrics = []Metric{
	{
		Code: "territory.population", Title: "Население", Unit: "чел.", Levels: territoryLevels, Version: 1,
		Formula: "численность на 1 января последнего года; округ — сумма районов",
		Method: "Росстат, база показателей муниципальных образований (численность постоянного населения на 1 января). " +
			"Для округа — сумма районов за один год; для Москвы — значение по городу.",
		Domains: []string{"statistics", "territory"},
		Compute: areas + `,
		p AS (SELECT DISTINCT ON (area_id) area_id, year, value FROM territory.population
		      WHERE kind = 'january1' AND area_id IS NOT NULL ORDER BY area_id, year DESC)
		SELECT a.level, a.entity, NULL::date,
		       CASE a.kind WHEN 'okrug' THEN (SELECT sum(p2.value) FROM territory.admin_area d JOIN p p2 ON p2.area_id = d.id
		                                      WHERE d.parent_id = a.id)
		            ELSE p.value END::float8,
		       jsonb_build_object('year', CASE a.kind WHEN 'okrug' THEN (SELECT max(p2.year) FROM territory.admin_area d
		                                      JOIN p p2 ON p2.area_id = d.id WHERE d.parent_id = a.id) ELSE p.year END)
		FROM a LEFT JOIN p ON p.area_id = a.id`,
	},
	{
		Code: "territory.facilities", Title: "Объектов логистики", Unit: "объектов", Levels: territoryLevels, Version: 1,
		Formula: "число действующих объектов реестра на территории",
		Method:  "Объекты реестра (склады, логистические и распределительные центры, терминалы и др.) по привязке к району и округу.",
		Domains: []string{"facility"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, count(f.id)::float8,
		       jsonb_build_object('building_m2', round(coalesce(sum(f.building_area_m2), 0)::numeric))
		FROM a LEFT JOIN infra.facility f ON f.status = 'active' AND ` + facilityIn + `
		GROUP BY a.level, a.entity`,
	},
	{
		Code: "territory.warehouse_density", Title: "Плотность складских площадей", Unit: "м²/км²", Levels: territoryLevels,
		Version: 1, Formula: "Σ площади застройки объектов реестра / площадь территории",
		Method:  "Площадь застройки — по контурам зданий объектов реестра (OSM); площадь территории — по границе OSM.",
		Domains: []string{"facility", "territory"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, (coalesce(sum(f.building_area_m2), 0) / a.area_km2)::float8,
		       jsonb_build_object('building_m2', round(coalesce(sum(f.building_area_m2), 0)::numeric), 'area_km2', round(a.area_km2::numeric, 2))
		FROM a LEFT JOIN infra.facility f ON f.status = 'active' AND ` + facilityIn + `
		GROUP BY a.level, a.entity, a.area_km2`,
	},
	{
		Code: "territory.warehouse_per_1000", Title: "Складские площади на 1000 жителей", Unit: "м²", Levels: territoryLevels,
		Version: 1, Formula: "Σ площади застройки объектов / население × 1000",
		Method: "Площадь застройки объектов реестра на 1000 жителей (население — показатель «Население»). " +
			"В районах с малым населением (промзоны) значение велико — это свойство показателя.",
		Domains:   []string{"facility"},
		DependsOn: []string{"territory.population"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, (coalesce(sum(f.building_area_m2), 0) / pop.value * 1000)::float8,
		       jsonb_build_object('building_m2', round(coalesce(sum(f.building_area_m2), 0)::numeric), 'population', pop.value)
		FROM a
		JOIN analytics.metric_value pop ON pop.metric_code = 'territory.population' AND pop.level = a.level
		     AND pop.entity = a.entity AND pop.value > 0
		LEFT JOIN infra.facility f ON f.status = 'active' AND ` + facilityIn + `
		GROUP BY a.level, a.entity, pop.value`,
	},
	{
		Code: "territory.industrial_share", Title: "Доля промзон", Unit: "%", Levels: territoryLevels, Version: 1,
		Formula: "площадь промзон в границах территории / площадь территории × 100",
		Method:  "Промзоны — участки OSM `landuse=industrial`; учитывается их часть внутри границы территории.",
		Domains: []string{"infra", "territory"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date,
		       (coalesce(sum(ST_Area(ST_Intersection(z.geom, a.geom)::geography)), 0) / 1e6 / a.area_km2 * 100)::float8,
		       jsonb_build_object('zones', count(z.id), 'area_km2', round(a.area_km2::numeric, 2))
		FROM a LEFT JOIN infra.industrial_zone z ON z.geom && a.geom AND ST_Intersects(z.geom, a.geom)
		GROUP BY a.level, a.entity, a.area_km2`,
	},
	{
		Code: "territory.network_km", Title: "Протяжённость улично-дорожной сети", Unit: "км", Levels: territoryLevels, Version: 1,
		Formula: "Σ длины участков OSM классов от магистралей до жилых улиц, середина которых на территории",
		Method: "Участки дорожной сети OSM без служебных проездов и полевых дорог; участок относится к территории, " +
			"где лежит его середина.",
		Domains: []string{"network", "territory"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, coalesce(sum(s.length_m), 0)::float8 / 1000,
		       jsonb_build_object('segments', count(s.id),
		                          'main_km', round((coalesce(sum(s.length_m) FILTER (WHERE s.class IN (` + mainClasses + `)), 0) / 1000)::numeric, 1))
		FROM a LEFT JOIN network.road_segment s ON s.class IN (` + streetClasses + `) AND ` + midpointIn + `
		GROUP BY a.level, a.entity`,
	},
	{
		Code: "territory.restricted_km", Title: "Сеть с ограничениями для грузовиков", Unit: "км", Levels: territoryLevels,
		Version: 1, Formula: "Σ длины участков улично-дорожной сети с запретом грузовиков или пределом массы и габаритов",
		Method: "Ограничения из тегов OSM и реестра дорожных знаков (запрет грузовиков и механических ТС, пределы массы, " +
			"нагрузки на ось, высоты, ширины, длины); включая действующие по времени.",
		Domains: []string{"network", "territory"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, coalesce(sum(s.length_m), 0)::float8 / 1000, jsonb_build_object('segments', count(s.id))
		FROM a LEFT JOIN network.road_segment s ON s.class IN (` + streetClasses + `) AND ` + midpointIn + `
		     AND EXISTS (SELECT 1 FROM network.restriction r WHERE r.segment_id = s.id AND r.kind IN (` + restrictionKinds + `))
		GROUP BY a.level, a.entity`,
	},
	{
		Code: "territory.restricted_share", Title: "Доля сети с ограничениями", Unit: "%", Levels: territoryLevels, Version: 1,
		Formula:   "сеть с ограничениями / протяжённость улично-дорожной сети × 100",
		Method:    "Отношение показателей «Сеть с ограничениями для грузовиков» и «Протяжённость улично-дорожной сети».",
		DependsOn: []string{"territory.restricted_km", "territory.network_km"},
		Compute: `SELECT r.level, r.entity, NULL::date, (r.value / n.value * 100)::float8,
		       jsonb_build_object('restricted_km', round(r.value::numeric, 1), 'network_km', round(n.value::numeric, 1))
		FROM analytics.metric_value r
		JOIN analytics.metric_value n ON n.metric_code = 'territory.network_km' AND n.level = r.level AND n.entity = r.entity AND n.value > 0
		WHERE r.metric_code = 'territory.restricted_km'`,
	},
	{
		Code: "territory.works_per_100km", Title: "Интенсивность дорожных работ", Unit: "работ на 100 км", Levels: territoryLevels,
		Version: 1, Daily: true, Formula: "идущие дорожные работы на территории / протяжённость сети × 100",
		Method: "Работы портала открытых данных Москвы, действующие на дату расчёта; работа относится к территории, " +
			"где лежит точка на её геометрии.",
		Domains:   []string{"operations"},
		DependsOn: []string{"territory.network_km"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, (count(w.id) / n.value * 100)::float8,
		       jsonb_build_object('works', count(w.id), 'network_km', round(n.value::numeric, 1), 'date', current_date)
		FROM a
		JOIN analytics.metric_value n ON n.metric_code = 'territory.network_km' AND n.level = a.level AND n.entity = a.entity AND n.value > 0
		LEFT JOIN operations.road_work w ON w.state = 'active' AND w.removed_at IS NULL AND w.geom IS NOT NULL
		     AND w.geom && a.geom AND ST_Intersects(a.geom, ST_PointOnSurface(w.geom))
		GROUP BY a.level, a.entity, n.value`,
	},
	{
		Code: "territory.parking_per_100km", Title: "Обеспеченность стоянками и отдыхом", Unit: "точек на 100 км",
		Levels: territoryLevels, Version: 1,
		Formula:   "стоянки грузовиков и площадки отдыха на территории / протяжённость магистральной сети × 100",
		Method:    "Магистральная сеть — участки OSM от магистралей до улиц районного значения (`motorway`–`tertiary`).",
		Domains:   []string{"infra"},
		DependsOn: []string{"territory.network_km"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, (count(p.id) / (n.inputs ->> 'main_km')::float8 * 100)::float8,
		       jsonb_build_object('points', count(p.id), 'main_km', n.inputs -> 'main_km')
		FROM a
		JOIN analytics.metric_value n ON n.metric_code = 'territory.network_km' AND n.level = a.level AND n.entity = a.entity
		     AND (n.inputs ->> 'main_km')::float8 > 0
		LEFT JOIN infra.poi p ON p.kind IN ('truck_parking', 'rest_area') AND p.point && a.geom AND ST_Intersects(a.geom, p.point)
		GROUP BY a.level, a.entity, n.inputs`,
	},
	{
		Code: "territory.fuel_per_100km", Title: "Обеспеченность АЗС", Unit: "АЗС на 100 км", Levels: territoryLevels,
		Version: 1, Formula: "АЗС на территории / протяжённость магистральной сети × 100",
		Method:    "АЗС — реестры Москвы (экологичные и прочие) и OSM; магистральная сеть — как у «Обеспеченности стоянками».",
		Domains:   []string{"infra"},
		DependsOn: []string{"territory.network_km"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, (count(p.id) / (n.inputs ->> 'main_km')::float8 * 100)::float8,
		       jsonb_build_object('points', count(p.id), 'main_km', n.inputs -> 'main_km')
		FROM a
		JOIN analytics.metric_value n ON n.metric_code = 'territory.network_km' AND n.level = a.level AND n.entity = a.entity
		     AND (n.inputs ->> 'main_km')::float8 > 0
		LEFT JOIN infra.poi p ON p.kind = 'fuel' AND p.point && a.geom AND ST_Intersects(a.geom, p.point)
		GROUP BY a.level, a.entity, n.inputs`,
	},
	{
		Code: "territory.median_mkad_min", Title: "Средняя доступность объектов", Unit: "мин", Levels: territoryLevels, Version: 1,
		Formula:   "медиана времени до МКАД объектов территории",
		Method:    "Медиана показателя «Время до МКАД» по объектам реестра территории; недостижимые объекты не учитываются.",
		DependsOn: []string{"facility.mkad_min"},
		Compute: areas + `
		SELECT a.level, a.entity, NULL::date, percentile_cont(0.5) WITHIN GROUP (ORDER BY v.value)::float8,
		       jsonb_build_object('facilities', count(v.value))
		FROM a
		JOIN infra.facility f ON f.status = 'active' AND ` + facilityIn + `
		JOIN analytics.metric_value v ON v.metric_code = 'facility.mkad_min' AND v.level = 'facility' AND v.entity = f.id::text
		GROUP BY a.level, a.entity`,
	},
}
