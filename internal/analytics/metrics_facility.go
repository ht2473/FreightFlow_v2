package analytics

// accessMetric — время грузовика 20 т от объекта до цели (расчёт доступности, Б4).
func accessMetric(code, title, target, targetText string) Metric {
	return Metric{
		Code: code, Title: title, Unit: "мин", Levels: []string{LevelFacility}, Version: 1,
		Formula: "время маршрута грузовика 20 т от точки въезда объекта до " + targetText,
		Method: "Маршрут по графу Valhalla с ограничениями из знаков и политикой зон (профиль «грузовик 20 т»: " +
			"масса 20 т, высота 4 м, ширина 2,55 м, длина 12 м), точка объекта привязывается к сети в радиусе 300 м. " +
			"Цель — " + targetText + ". Недостижимые объекты значения не имеют.",
		Domains: []string{"accessibility"},
		Compute: `SELECT 'facility', a.facility_id::text, NULL::date, a.minutes::float8,
			       jsonb_build_object('km', round(a.km::numeric, 1), 'profile', a.profile_code, 'graph', a.graph_built_at)
			FROM analytics.accessibility a
			JOIN infra.facility f ON f.id = a.facility_id AND f.status = 'active'
			WHERE a.profile_code = 'truck_20' AND a.target_kind = '` + target + `' AND a.minutes IS NOT NULL`,
	}
}

// restrictionKinds — ограничения, закрывающие или стесняющие проезд грузовика.
const restrictionKinds = `'hgv_ban', 'motor_vehicle_ban', 'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength'`

var facilityMetrics = []Metric{
	accessMetric("facility.mkad_min", "Время до МКАД", "mkad", "ближайшей развязки МКАД"),
	accessMetric("facility.ttk_min", "Время до ТТК", "ttk", "ближайшего въезда на ТТК"),
	accessMetric("facility.center_min", "Время до центра", "center", "центра (Кремль)"),
	accessMetric("facility.rail_min", "Время до ж/д грузового пункта", "rail_freight", "ближайшего ж/д грузового пункта"),
	{
		Code: "facility.floor_area", Title: "Общая площадь (оценка)", Unit: "м²", Levels: []string{LevelFacility}, Version: 1,
		Formula: "Σ по зданиям объекта: площадь застройки × этажность",
		Method: "Площадь застройки — по контурам зданий OSM; этажность — тег `building:levels`, без него — 1 этаж. " +
			"Это модельная оценка: антресоли, стеллажи и неотапливаемые части не учитываются.",
		Domains: []string{"facility"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, f.floor_area_m2::float8,
			       jsonb_build_object('building_m2', f.building_area_m2, 'levels', f.levels, 'buildings', f.buildings)
			FROM infra.facility f WHERE f.status = 'active' AND f.floor_area_m2 IS NOT NULL`,
	},
	{
		Code: "facility.confidence", Title: "Достоверность записи", Unit: "0–1", Levels: []string{LevelFacility}, Version: 1,
		Formula: "сумма весов признаков источников, не больше 1",
		Method: "Признаки: тип по тегам OSM, название, адрес из адресного реестра, разрешение на ввод, совпадение " +
			"с промзоной, площадь здания. Веса и причины — в карточке объекта (вкладка «Источники»).",
		Domains: []string{"facility"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, f.confidence::float8, coalesce(f.confidence_reasons, '{}'::jsonb)
			FROM infra.facility f WHERE f.status = 'active'`,
	},
	{
		Code: "facility.restrictions_300m", Title: "Участков с ограничениями рядом", Unit: "участков", Levels: []string{LevelFacility},
		Version: 1, Formula: "число участков сети в 300 м от объекта с запретом грузовиков или пределом массы и габаритов",
		Method: "Участки дорожной сети OSM в радиусе 300 м от точки объекта, на которых действует запрет движения " +
			"грузовиков или механических ТС, предел массы, нагрузки на ось, высоты, ширины или длины (из тегов OSM " +
			"и реестра дорожных знаков). Показывает стеснённость подъездов, но не путь въезда.",
		Domains: []string{"facility", "network"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, count(DISTINCT r.segment_id)::float8,
			       jsonb_build_object('kinds', coalesce(jsonb_agg(DISTINCT r.kind) FILTER (WHERE r.kind IS NOT NULL), '[]'))
			FROM infra.facility f
			LEFT JOIN LATERAL (
				SELECT s.id FROM network.road_segment s
				WHERE s.geom && ST_Expand(f.point, 0.006) AND ST_DWithin(s.geom::geography, f.point::geography, 300)
			) s ON true
			LEFT JOIN network.restriction r ON r.segment_id = s.id AND r.kind IN (` + restrictionKinds + `)
			WHERE f.status = 'active'
			GROUP BY f.id`,
	},
	{
		Code: "facility.works_500m", Title: "Дорожные работы рядом", Unit: "работ", Levels: []string{LevelFacility}, Version: 1,
		Daily:   true,
		Formula: "число идущих дорожных работ в 500 м от объекта",
		Method: "Работы портала открытых данных Москвы (плановые и аварийные), действующие на дату расчёта, геометрия " +
			"которых проходит в 500 м от объекта.",
		Domains: []string{"operations", "facility"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, count(w.id)::float8,
			       jsonb_build_object('nearest_m', round(min(rf.distance_m)::numeric))
			FROM infra.facility f
			LEFT JOIN operations.road_work_facility rf ON rf.facility_id = f.id
			LEFT JOIN operations.road_work w ON w.id = rf.work_id AND w.state = 'active' AND w.removed_at IS NULL
			WHERE f.status = 'active'
			GROUP BY f.id`,
	},
	{
		Code: "facility.infra_3km", Title: "Инфраструктура для грузовиков рядом", Unit: "точек", Levels: []string{LevelFacility},
		Version: 1, Formula: "число АЗС, стоянок грузовиков и площадок отдыха в 3 км",
		Method: "Точки инфраструктуры из реестров Москвы (АЗС, стоянки грузового транспорта) и OSM в радиусе 3 км " +
			"по прямой от объекта.",
		Domains: []string{"infra", "facility"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, count(p.id)::float8,
			       jsonb_build_object('fuel', count(p.id) FILTER (WHERE p.kind = 'fuel'),
			                          'truck_parking', count(p.id) FILTER (WHERE p.kind = 'truck_parking'),
			                          'rest_area', count(p.id) FILTER (WHERE p.kind = 'rest_area'))
			FROM infra.facility f
			LEFT JOIN infra.poi p ON p.kind IN ('fuel', 'truck_parking', 'rest_area')
			     AND p.point && ST_Expand(f.point, 0.05) AND ST_DWithin(p.point::geography, f.point::geography, 3000)
			WHERE f.status = 'active'
			GROUP BY f.id`,
	},
	{
		Code: "facility.parking_km", Title: "До стоянки или площадки отдыха", Unit: "км", Levels: []string{LevelFacility},
		Version: 1, Formula: "расстояние по прямой до ближайшей стоянки грузовиков или площадки отдыха",
		Method: "Ближайшая точка видов «стоянка грузового транспорта» и «площадка отдыха» (реестр Москвы и OSM), " +
			"расстояние по прямой — нижняя граница пути по сети.",
		Domains: []string{"infra", "facility"},
		Compute: `SELECT 'facility', f.id::text, NULL::date, round((ST_Distance(p.point::geography, f.point::geography) / 1000)::numeric, 2)::float8,
			       jsonb_build_object('kind', p.kind, 'name', p.name)
			FROM infra.facility f
			CROSS JOIN LATERAL (
				SELECT p.point, p.kind, p.name FROM infra.poi p WHERE p.kind IN ('truck_parking', 'rest_area')
				ORDER BY p.point <-> f.point LIMIT 1
			) p
			WHERE f.status = 'active'`,
	},
}
