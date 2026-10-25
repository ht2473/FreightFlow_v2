package analytics

// latestAnalysis — последняя версия графа сетевого анализа.
const latestAnalysis = `(SELECT max(version_id) FROM network.edge_metric)`

var segmentMetrics = []Metric{
	{
		Code: "segment.significance", Title: "Значимость участка", Unit: "процентиль", Levels: []string{LevelSegment}, Version: 1,
		Formula: "процентиль рёберной центральности участка в грузовом графе (хранятся верхние 10 %)",
		Method: "Рёберная центральность по выборочному алгоритму Брандеса на графе для грузовика 12 т с ограничениями " +
			"(сетевой анализ, Р-45): доля кратчайших путей между объектами и границами города, проходящих через участок. " +
			"Значение — процентиль по всем рёбрам; хранятся участки не ниже 90-го процентиля, полный расчёт — в разделе " +
			"«Транспортная сеть».",
		Domains: []string{"network_analysis"},
		Compute: `SELECT 'segment', e.segment_id::text, NULL::date, max(m.percentile)::float8 * 100,
			       jsonb_build_object('rank', min(m.rank), 'bottleneck', bool_or(m.bottleneck), 'version', max(m.version_id))
			FROM network.edge_metric m
			JOIN network.graph_edge e ON e.version_id = m.version_id AND e.id = m.edge_id
			WHERE m.version_id = ` + latestAnalysis + ` AND m.percentile >= 0.9
			GROUP BY e.segment_id`,
	},
	{
		Code: "segment.criticality", Title: "Критичность элемента сети", Unit: "мин", Levels: []string{LevelSegment}, Version: 1,
		Formula: "суммарный рост времени доступа объектов при выпадении моста или участка",
		Method: "Для мостов и наиболее значимых участков моделируется их выпадение; считается рост времени от объектов " +
			"до МКАД и центра по графу анализа (часть проверена маршрутами Valhalla). Входы: число затронутых объектов, " +
			"наибольшая задержка, отрезанные объекты.",
		Domains: []string{"network_analysis"},
		Compute: `SELECT 'segment', c.kind || ':' || c.id, NULL::date, coalesce(c.valhalla_damage_min, c.damage_min)::float8,
			       jsonb_build_object('name', c.name, 'kind', c.kind, 'facilities', c.facilities, 'max_delay_min', c.max_delay_min,
			                          'disconnected', c.disconnected, 'confirmed', c.confirmed)
			FROM network.element_criticality c
			WHERE c.version_id = (SELECT max(version_id) FROM network.element_criticality)`,
	},
}

var cityMetrics = []Metric{
	{
		Code: "city.commissioning", Title: "Ввод объектов в эксплуатацию", Unit: "разрешений", Levels: []string{LevelCity}, Version: 1,
		Formula: "число разрешений на ввод в эксплуатацию за месяц",
		Method: "Реестр разрешений на ввод объектов в эксплуатацию (портал открытых данных Москвы); во входах — " +
			"разрешения на объекты логистики и их площадь.",
		Domains: []string{"facility"},
		Compute: `SELECT 'city', 'moscow', date_trunc('month', c.permit_date)::date, count(*)::float8,
			       jsonb_build_object('logistics', count(*) FILTER (WHERE c.logistics),
			                          'area_m2', round(coalesce(sum(c.area_m2), 0)::numeric))
			FROM infra.commissioning c WHERE c.permit_date IS NOT NULL
			GROUP BY date_trunc('month', c.permit_date)`,
	},
	{
		Code: "city.passes_valid", Title: "Действующие пропуска грузовиков", Unit: "пропусков", Levels: []string{LevelCity},
		Version: 1, Formula: "число действующих пропусков на въезд грузовиков в зоны ограничения на месяц",
		Method: "Статистика пропусков Дептранса (портал открытых данных Москвы): действующие пропуска всех видов " +
			"(разовые, годовые) в зоны МКАД, ТТК и Садового кольца; во входах — по зонам.",
		Domains: []string{"regulation"},
		Compute: `SELECT 'city', 'moscow', p.month, sum(p.value)::float8, jsonb_object_agg(p.zone, p.value)
			FROM (SELECT month, zone, sum(value) AS value FROM regulation.pass_stat WHERE metric = 'valid' GROUP BY month, zone) p
			GROUP BY p.month`,
	},
	{
		Code: "city.special_permits", Title: "Специальные разрешения на тяжеловесный и крупногабаритный транспорт",
		Unit: "разрешений", Levels: []string{LevelCity}, Version: 1,
		Formula: "число выданных специальных разрешений за месяц",
		Method:  "Реестр специальных разрешений на движение тяжеловесного и крупногабаритного транспорта (портал открытых данных Москвы).",
		Domains: []string{"regulation"},
		Compute: `SELECT 'city', 'moscow', date_trunc('month', s.issued_on)::date, count(*)::float8,
			       jsonb_build_object('trips', coalesce(sum(s.trips), 0))
			FROM regulation.special_permit s WHERE s.issued_on IS NOT NULL
			GROUP BY date_trunc('month', s.issued_on)`,
	},
	{
		Code: "city.vehicle_emissions", Title: "Выбросы от автомобильного транспорта", Unit: "в единицах набора",
		Levels: []string{LevelCity}, Version: 1, Formula: "значение набора за год",
		Method: "Выбросы загрязняющих веществ в атмосферу от автомобильного транспорта, портал открытых данных Москвы, " +
			"набор 2385. Единица измерения в паспорте набора не указана — значения показываются как есть.",
		Domains: []string{"statistics"},
		Compute: `SELECT 'city', 'moscow', o.period, o.value::float8, jsonb_build_object('source', o.source_code)
			FROM statistics.observation o WHERE o.indicator_code = 'vehicle_emissions' AND o.area_name = 'Москва'`,
	},
	{
		Code: "city.freight_tonnage", Title: "Перевозка грузов автомобильным транспортом", Unit: "тыс. т", Levels: []string{LevelCity},
		Version: 1, Formula: "объём перевезённых грузов за год (организации без субъектов малого предпринимательства)",
		Method:  "ЕМИСС, показатель 57791 по Москве.",
		Domains: []string{"statistics"},
		Compute: `SELECT 'city', 'moscow', o.period, o.value::float8, jsonb_build_object('source', o.source_code)
			FROM statistics.observation o WHERE o.indicator_code = 'emiss_57791'`,
	},
}
