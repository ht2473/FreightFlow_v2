package mosdata

import (
	"time"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/events"
)

const (
	day   = 24 * time.Hour
	week  = 7 * day
	month = 30 * day
)

// Datasets — подключённые наборы портала. Период — проверка версии набора: при неизменной
// версии загрузка не выполняется (один запрос). MinRecords — примерно половина объёма на
// момент подключения (13.10.2026): меньше — набор обрезан или пуст.
// worksGeometry — у работ должна быть геометрия, иначе их не показать на карте и не оценить влияние.
var worksGeometry = StatCheck{
	Code: "works_geometry", Title: "Геометрия есть почти у всех работ (без неё не больше 2 %)",
	Severity: ingest.SeverityWarning, Stat: "works_without_geometry_pct", Min: 0, Max: 2,
}

var Datasets = []Dataset{
	// Дорожная сеть и инфраструктура УДС.
	{
		ID: 62681, Code: "mos_road_signs", Title: "Дорожные знаки", Domains: []string{"network"},
		Interval: day, Geo: true, Transform: "road_signs.sql", MinRecords: 280000,
		Checks: []StatCheck{
			{
				Code: "signs_matched", Title: "Знаки ограничений привязаны к сети (не меньше 90 %)",
				Severity: ingest.SeverityWarning, Stat: "signs_match_pct", Min: 90, Max: 100,
			},
			{
				Code: "signs_recognized", Title: "Тип распознан почти у всех знаков (без номера не больше 1000)",
				Severity: ingest.SeverityWarning, Stat: "signs_unrecognized", Min: 0, Max: 1000,
			},
		},
	},
	{
		ID: 62801, Code: "mos_sign_works", Title: "Работы на дорожных знаках", Domains: []string{"network"},
		Interval: week, Geo: true, Transform: "sign_works.sql", MinRecords: 1500,
	},
	{
		ID: 2069, Code: "mos_bridges", Title: "Мосты", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "bridges.sql", MinRecords: 500,
	},
	{
		ID: 62059, Code: "mos_traffic_lights", Title: "Светофорные объекты", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "road_assets.sql", MinRecords: 2000,
	},
	{
		ID: 61701, Code: "mos_cameras", Title: "Комплексы фотовидеофиксации", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "road_assets.sql", MinRecords: 2000,
	},
	{
		ID: 674, Code: "mos_speed_bumps", Title: "Искусственные дорожные неровности", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "road_assets.sql", MinRecords: 300,
	},
	{
		ID: 62662, Code: "mos_dedicated_lanes", Title: "Выделенные полосы", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "road_assets.sql", MinRecords: 140,
	},
	{
		ID: 62747, Code: "mos_info_boards", Title: "Табло на улично-дорожной сети", Domains: []string{"network"},
		Interval: month, Geo: true, Transform: "road_assets.sql", MinRecords: 300,
	},
	{
		ID: 64032, Code: "mos_road_objects", Title: "Объекты дорожного хозяйства (текущее содержание)", Domains: []string{"network"},
		Interval: week, Geo: true, Transform: "road_assets.sql", MinRecords: 4000,
	},

	// Дорожные работы и перекрытия; история 2016–2019 загружается вручную один раз.
	{
		Checks: []StatCheck{worksGeometry},
		ID:     62101, Code: "mos_works_planned", Title: "Запланированные дорожные ремонтные работы", Domains: []string{"operations"},
		Interval: day, Geo: true, Transform: "road_works.sql", MinRecords: 2400, Event: events.RoadworkChanged,
	},
	{
		Checks: []StatCheck{worksGeometry},
		ID:     62461, Code: "mos_works_emergency", Title: "Аварийно-восстановительный ремонт коммуникаций и дорог", Domains: []string{"operations"},
		Interval: day, Geo: true, Transform: "road_works.sql", MinRecords: 1000, Event: events.RoadworkChanged,
	},
	{
		ID: 2527, Code: "mos_traffic_closures", Title: "Ограничения движения на период работ", Domains: []string{"operations"},
		Interval: day, Geo: true, Transform: "road_works.sql", MinRecords: 1, Event: events.RoadworkChanged,
	},
	{
		ID: 60601, Code: "mos_works_2018", Title: "Плановые дорожные работы 2018 года", Domains: []string{"operations"},
		Geo: true, Transform: "road_works.sql", MinRecords: 300,
	},
	{
		ID: 61581, Code: "mos_works_2019", Title: "Плановые дорожные работы 2019 года", Domains: []string{"operations"},
		Geo: true, Transform: "road_works.sql", MinRecords: 300,
	},
	{
		ID: 2468, Code: "mos_works_2016", Title: "Выполнение плана дорожных работ 2016 года", Domains: []string{"operations"},
		Geo: true, Transform: "road_works.sql", MinRecords: 250,
	},
	{
		ID: 3141, Code: "mos_works_2017", Title: "Выполнение плана дорожных работ 2017 года", Domains: []string{"operations"},
		Geo: true, Transform: "road_works.sql", MinRecords: 250,
	},

	// Регулирование: только нужные поля — без номеров пропусков, перевозчиков и ТС.
	{
		ID: 3247, Code: "mos_passes", Title: "Пропуска грузового транспорта (агрегаты)", Domains: []string{"regulation"},
		Interval: week, Fields: []string{"Series", "ValidFrom", "ValidTo", "Zone", "Status", "CancelDate"},
		Transform: "passes.sql", MinRecords: 500000,
	},
	{
		ID: 2582, Code: "mos_special_permits", Title: "Спецразрешения на тяжеловесные и крупногабаритные ТС", Domains: []string{"regulation"},
		Interval: week, Fields: []string{
			"TypeOfTransportation", "CarrierType", "NumberOfTrips", "PeriodOfValidity",
			"AutotrainLength", "AutotrainWidth", "AutotrainHeight", "AutotrainWeightWithoutCargo",
			"AutotrainWeightWithCargo", "IssuedDate",
		},
		Transform: "special_permits.sql", MinRecords: 1500,
	},

	// Статистика по городу.
	{
		ID: 62021, Code: "mos_population", Title: "Численность постоянного населения в среднем за год", Domains: []string{"statistics"},
		Interval: month, Transform: "statistics.sql", MinRecords: 5,
	},
	{
		ID: 2385, Code: "mos_emissions", Title: "Выбросы загрязняющих веществ от автомобильного транспорта", Domains: []string{"statistics"},
		Interval: month, Transform: "statistics.sql", MinRecords: 10,
	},

	// Территория: адреса, ограничения для размещения, ввод объектов.
	{
		Checks: []StatCheck{{
			Code: "addresses_geometry", Title: "Контуры есть почти у всех зданий и сооружений (без контура не больше 1 %)",
			Severity: ingest.SeverityWarning, Stat: "buildings_without_geometry_pct", Min: 0, Max: 1,
		}},
		ID: 60562, Code: "mos_addresses", Title: "Адресный реестр объектов недвижимости", Domains: []string{"territory"},
		Interval: week, Geo: true, Transform: "addresses.sql", MinRecords: 300000,
	},
	{
		ID: 64028, Code: "mos_commissioning", Title: "Разрешения на ввод объектов в эксплуатацию", Domains: []string{"facility"},
		Interval: week, Fields: []string{
			"PermissionNumber", "PermissionDate", "ObjectName", "AdmArea", "District", "Address",
			"WorkType", "FunctionalPurpose", "FundingSource", "ObjectArea", "ObjectLength",
		},
		Transform: "commissioning.sql", MinRecords: 2000,
	},
	{
		ID: 613, Code: "mos_heritage_zones", Title: "Зоны охраны объектов культурного наследия", Domains: []string{"territory"},
		Interval: month, Geo: true, Transform: "constraint_zones.sql", MinRecords: 400,
	},
	{
		ID: 614, Code: "mos_heritage_territories", Title: "Территории объектов культурного наследия", Domains: []string{"territory"},
		Interval: month, Geo: true, Transform: "constraint_zones.sql", MinRecords: 1500,
	},
	{
		ID: 1465, Code: "mos_parks", Title: "Парковые территории", Domains: []string{"territory"},
		Interval: month, Geo: true, Transform: "constraint_zones.sql", MinRecords: 700,
	},

	// Точечная инфраструктура и точки притяжения.
	{
		ID: 2984, Code: "mos_truck_parking", Title: "Стоянки для большегрузного автотранспорта", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 10,
	},
	{
		ID: 61087, Code: "mos_rest_areas", Title: "Площадки отдыха водителей", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 80,
	},
	{
		ID: 753, Code: "mos_fuel_eco", Title: "АЗС с топливом, соответствующим экотребованиям", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 150,
	},
	{
		ID: 754, Code: "mos_fuel_noneco", Title: "АЗС с топливом, не соответствующим экотребованиям", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 1,
	},
	{
		ID: 1047, Code: "mos_tpu", Title: "Транспортно-пересадочные узлы", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 100,
	},
	{
		ID: 3290, Code: "mos_tpu_special", Title: "ТПУ особого режима содержания", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 50,
	},
	{
		ID: 62201, Code: "mos_rail_terminals", Title: "Железнодорожные вокзалы", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 5,
	},
	{
		ID: 2254, Code: "mos_technoparks", Title: "Технопарки", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 15,
	},
	{
		ID: 654, Code: "mos_markets", Title: "Розничные рынки", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 10,
	},
	{
		ID: 653, Code: "mos_fairs_regional", Title: "Региональные ярмарки", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 1,
	},
	{
		ID: 62061, Code: "mos_fairs_interregional", Title: "Межрегиональные ярмарки", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 1,
	},
	{
		ID: 620, Code: "mos_fairs_weekend", Title: "Ярмарки выходного дня", Domains: []string{"infra"},
		Interval: month, Geo: true, Transform: "poi.sql", MinRecords: 1,
	},
}
