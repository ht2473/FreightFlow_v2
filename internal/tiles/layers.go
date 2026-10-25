package tiles

import "freightflow/internal/identity"

// Layer — слой данных карты. Тайлы строит SQL-функция tiles.<Code>(z, x, y) (миграции);
// клиент получает описание через каталог и строит стиль из токенов своей темы.
type Layer struct {
	Code        string
	Group       string
	Title       string
	Description string
	// Geometry — point, line или polygon.
	Geometry string
	// MinZoom — с какого масштаба слой показывается; MaxZoom — последний масштаб тайлов
	// (дальше клиент увеличивает имеющиеся).
	MinZoom, MaxZoom int
	Permission       identity.Permission
	// Domains — домены данных: их версии входят в адрес тайлов и ключ кеша.
	Domains     []string
	Attribution string
	// Visible — включён по умолчанию.
	Visible    bool
	Style      Style
	Properties []Property
}

// Style — правило оформления. Цвета — роли токенов дизайна (acc, n9, warn, crit, info,
// cat1…cat8), их значения клиент берёт из своей темы.
type Style struct {
	// Kind — single (один цвет) или category (цвет по значению свойства).
	Kind       string
	Color      string
	Property   string
	Categories []Category
	// Dashed — свойство-признак, при котором линия пунктирная.
	DashedBy string
	// Note — пояснение к легенде (правило размера, пунктира).
	Note string
}

// Category — значение свойства в легенде.
type Category struct {
	Values []string
	Label  string
	Color  string
}

// Property — свойство объекта в тайле для подсказки и карточки.
type Property struct {
	Name  string
	Title string
	// Kind — text, number, bool или enum.
	Kind string
	Unit string
	// Labels — подписи значений перечисления.
	Labels map[string]string
}

// Группы слоёв в порядке показа.
var Groups = []struct{ Code, Title string }{
	{"objects", "Объекты"},
	{"infra", "Инфраструктура"},
	{"network", "Сеть и ограничения"},
	{"operations", "Оперативная обстановка"},
	{"territory", "Территории"},
}

const (
	osmAttribution = "© участники OpenStreetMap"
	mosAttribution = "Портал открытых данных Правительства Москвы, data.mos.ru (CC BY 4.0)"
	// mixedAttribution — слои, где официальные записи дополнены записями OSM.
	mixedAttribution = mosAttribution + "; " + osmAttribution
	// dtpAttribution — ДТП по данным ГИБДД в обработке «Карты ДТП».
	dtpAttribution = "«Карта ДТП», dtp-stat.ru (данные ГИБДД)"
	// cargoFrameAttribution — перечень улиц Дептранса, сопоставленный с участками OSM.
	cargoFrameAttribution = "Дептранс Москвы, transport.mos.ru; " + osmAttribution
)

// facilityTypes — подписи типов объектов (справочник ref.facility_type).
var facilityTypes = map[string]string{
	"logistics_center": "Логистический центр",
	"warehouse":        "Склад",
	"cold_storage":     "Холодильный склад",
	"terminal":         "Грузовой терминал",
	"sorting_center":   "Сортировочный центр",
	"wholesale":        "Оптовая торговля",
	"rail_freight":     "Ж/д грузовой объект",
	"port":             "Речной порт",
}

var poiKinds = map[string]string{
	"fuel":          "АЗС",
	"truck_parking": "Стоянка грузовиков",
	"rest_area":     "Площадка отдыха",
	"marketplace":   "Рынок",
	"fair":          "Ярмарка",
	"rail_station":  "Вокзал",
	"rail_freight":  "Ж/д грузовой пункт",
	"customs":       "Таможня",
	"port":          "Порт",
	"tpu":           "Транспортно-пересадочный узел",
	"technopark":    "Технопарк",
}

// limitKinds — пределы массы и габаритов.
var limitKinds = map[string]string{
	"maxheight": "высота", "maxweight": "масса", "maxaxleload": "нагрузка на ось", "maxwidth": "ширина", "maxlength": "длина",
}

var poiProps = []Property{
	{Name: "name", Title: "Название", Kind: "text"},
	{Name: "kind", Title: "Вид", Kind: "enum", Labels: poiKinds},
	{Name: "address", Title: "Адрес", Kind: "text"},
	{Name: "operator", Title: "Оператор", Kind: "text"},
	{Name: "opening_hours", Title: "Часы работы", Kind: "text"},
	{Name: "capacity", Title: "Мест", Kind: "number"},
	{Name: "source", Title: "Источник записи", Kind: "enum", Labels: map[string]string{
		"mos": "Портал открытых данных Москвы", "osm": "OpenStreetMap",
	}},
}

var incidentProps = []Property{
	{Name: "occurred", Title: "Дата и время", Kind: "text"},
	{Name: "category", Title: "Вид ДТП", Kind: "text"},
	{Name: "severity", Title: "Тяжесть", Kind: "enum", Labels: map[string]string{
		"fatal": "с погибшими", "severe": "тяжёлый вред здоровью", "light": "лёгкий вред здоровью", "none": "без пострадавших",
	}},
	{Name: "dead", Title: "Погибших", Kind: "number"},
	{Name: "injured", Title: "Раненых", Kind: "number"},
	{Name: "vehicles", Title: "Транспортные средства", Kind: "text"},
	{Name: "address", Title: "Адрес", Kind: "text"},
}

// Registry — слои карты в порядке отрисовки снизу вверх.
var Registry = []Layer{
	{
		Code: "boundaries", Group: "territory", Title: "Округа и районы",
		Description: "Границы Москвы, административных округов и районов", Geometry: "polygon",
		MinZoom: 7, MaxZoom: 14, Permission: identity.PermMapView, Domains: []string{"territory"},
		Attribution: osmAttribution, Visible: true,
		Style: Style{Kind: "single", Color: "n10"},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "kind", Title: "Уровень", Kind: "enum", Labels: map[string]string{
				"region": "Город", "okrug": "Административный округ", "district": "Район",
			}},
			{Name: "area_km2", Title: "Площадь", Kind: "number", Unit: "км²"},
		},
	},
	{
		Code: "h3_cells", Group: "territory", Title: "Складские площади по ячейкам H3",
		Description: "Ячейки H3 с объектами логистики (разрешение 8, с 11-го масштаба — 9): цвет — площадь " +
			"застройки объектов; в карточке — показатели ячейки", Geometry: "polygon",
		MinZoom: 8, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"analytics"},
		Attribution: osmAttribution + "; Росстат (население)",
		Style: Style{Kind: "category", Property: "class", Note: "Население — модель: по зданиям адресного реестра", Categories: []Category{
			{Values: []string{"large"}, Label: "От 50 000 м²", Color: "crit"},
			{Values: []string{"medium"}, Label: "10 000–50 000 м²", Color: "warn"},
			{Values: []string{"small"}, Label: "До 10 000 м²", Color: "cat3"},
		}},
		Properties: []Property{
			{Name: "facilities", Title: "Объектов логистики", Kind: "number"},
			{Name: "building_area", Title: "Площадь застройки объектов", Kind: "number", Unit: "м²"},
			{Name: "population", Title: "Население (модель)", Kind: "number", Unit: "чел."},
			{Name: "infra", Title: "АЗС, стоянок, площадок отдыха", Kind: "number"},
			{Name: "restricted_km", Title: "Сеть с ограничениями", Kind: "number", Unit: "км"},
			{Name: "works", Title: "Идущих дорожных работ", Kind: "number"},
			{Name: "res", Title: "Разрешение H3", Kind: "number"},
		},
	},
	{
		Code: "industrial_zones", Group: "territory", Title: "Промзоны",
		Description: "Территории промышленного назначения по разметке OSM", Geometry: "polygon",
		MinZoom: 9, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: osmAttribution,
		Style:       Style{Kind: "single", Color: "n9"},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "area_ha", Title: "Площадь", Kind: "number", Unit: "га"},
		},
	},
	{
		Code: "constraint_zones", Group: "territory", Title: "Ограничения для размещения",
		Description: "Зоны охраны и территории объектов культурного наследия, парковые территории", Geometry: "polygon",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"territory"},
		Attribution: mosAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"heritage_protection"}, Label: "Зона охраны ОКН", Color: "cat4"},
			{Values: []string{"heritage_territory"}, Label: "Территория ОКН", Color: "cat2"},
			{Values: []string{"park"}, Label: "Парк", Color: "cat3"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "kind", Title: "Вид", Kind: "enum", Labels: map[string]string{
				"heritage_protection": "Зона охраны объекта культурного наследия",
				"heritage_territory":  "Территория объекта культурного наследия",
				"park":                "Парковая территория",
			}},
			{Name: "area_ha", Title: "Площадь", Kind: "number", Unit: "га"},
		},
	},
	{
		Code: "rings", Group: "territory", Title: "Кольца",
		Description: "Садовое кольцо, ТТК, МКАД, ЦКАД — границы зон", Geometry: "line",
		MinZoom: 7, MaxZoom: 14, Permission: identity.PermMapView, Domains: []string{"territory"},
		Attribution: osmAttribution, Visible: true,
		Style: Style{Kind: "single", Color: "n11"},
		Properties: []Property{
			{Name: "name", Title: "Кольцо", Kind: "text"},
			{Name: "length_km", Title: "Длина", Kind: "number", Unit: "км"},
		},
	},
	{
		Code: "roads", Group: "network", Title: "Дорожная сеть",
		Description: "Участки дорог OSM по классам; на мелких масштабах — только магистрали", Geometry: "line",
		MinZoom: 8, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: osmAttribution,
		Style: Style{Kind: "category", Property: "class", Categories: []Category{
			{Values: []string{"motorway", "trunk"}, Label: "Магистрали", Color: "cat2"},
			{Values: []string{"primary"}, Label: "Главные", Color: "cat5"},
			{Values: []string{"secondary"}, Label: "Второстепенные", Color: "cat1"},
			{Values: []string{"tertiary"}, Label: "Местного значения", Color: "cat3"},
			{Values: []string{"unclassified", "residential", "living_street", "service", "track"}, Label: "Улицы и проезды", Color: "n9"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "ref", Title: "Номер", Kind: "text"},
			{Name: "class", Title: "Класс", Kind: "enum", Labels: map[string]string{
				"motorway": "Автомагистраль", "trunk": "Скоростная", "primary": "Главная", "secondary": "Второстепенная",
				"tertiary": "Местного значения", "unclassified": "Прочая", "residential": "Жилая улица",
				"living_street": "Жилая зона", "service": "Проезд", "track": "Грунтовая",
			}},
		},
	},
	{
		Code: "bridges", Group: "network", Title: "Мосты и путепроводы",
		Description: "Участки дорог на мостах и эстакадах", Geometry: "line",
		MinZoom: 12, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: osmAttribution,
		Style:       Style{Kind: "single", Color: "n12"},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "kind", Title: "Вид", Kind: "enum", Labels: map[string]string{
				"bridge": "Мост", "viaduct": "Эстакада",
			}},
			{Name: "length_m", Title: "Длина", Kind: "number", Unit: "м"},
			{Name: "maxweight", Title: "Ограничение массы", Kind: "number", Unit: "т"},
			{Name: "maxheight", Title: "Ограничение высоты", Kind: "number", Unit: "м"},
		},
	},
	{
		Code: "road_assets", Group: "network", Title: "Инфраструктура УДС",
		Description: "Светофоры, камеры фотовидеофиксации, искусственные неровности, выделенные полосы, табло", Geometry: "point",
		MinZoom: 13, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: mosAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"traffic_light"}, Label: "Светофор", Color: "cat3"},
			{Values: []string{"camera"}, Label: "Камера фотовидеофиксации", Color: "cat2"},
			{Values: []string{"speed_bump"}, Label: "Искусственная неровность", Color: "cat5"},
			{Values: []string{"dedicated_lane"}, Label: "Выделенная полоса", Color: "cat1"},
			{Values: []string{"info_board"}, Label: "Табло", Color: "cat6"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "kind", Title: "Вид", Kind: "enum", Labels: map[string]string{
				"traffic_light": "Светофорный объект", "camera": "Камера фотовидеофиксации",
				"speed_bump": "Искусственная неровность", "dedicated_lane": "Выделенная полоса", "info_board": "Табло",
			}},
			{Name: "address", Title: "Адрес", Kind: "text"},
		},
	},
	{
		Code: "road_signs", Group: "network", Title: "Дорожные знаки",
		Description: "Реестр знаков Москвы; до 15-го масштаба — только знаки ограничений для грузовиков", Geometry: "point",
		MinZoom: 12, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: mosAttribution,
		Style: Style{
			Kind: "category", Property: "kind", Note: "Серым — прочие знаки (с 15-го масштаба)",
			Categories: []Category{
				{Values: []string{"hgv_ban", "motor_vehicle_ban", "hazmat_ban"}, Label: "Запрещающий движение", Color: "crit"},
				{Values: []string{"maxweight", "maxaxleload", "maxheight", "maxwidth", "maxlength"}, Label: "Ограничение массы или габарита", Color: "warn"},
			},
		},
		Properties: []Property{
			{Name: "name", Title: "Знак", Kind: "text"},
			{Name: "match_status", Title: "Привязка к сети", Kind: "enum", Labels: map[string]string{
				"matched": "привязан", "ambiguous": "неоднозначно", "unmatched": "не привязан", "pending": "ожидает",
			}},
			{Name: "temporary", Title: "Временный", Kind: "bool"},
			{Name: "location", Title: "Адресный ориентир", Kind: "text"},
		},
	},
	{
		Code: "restrictions", Group: "network", Title: "Ограничения для грузовиков",
		Description: "Запреты и пределы массы и габаритов: знаки Москвы и разметка OSM", Geometry: "line",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: mixedAttribution, Visible: true,
		Style: Style{
			Kind: "category", Property: "severity", DashedBy: "conditional",
			Note: "Пунктир — ограничение действует по времени",
			Categories: []Category{
				{Values: []string{"ban"}, Label: "Запрет движения", Color: "crit"},
				{Values: []string{"limit"}, Label: "Предел массы или габарита", Color: "warn"},
				{Values: []string{"access"}, Label: "Только для подъезда", Color: "info"},
			},
		},
		Properties: []Property{
			{Name: "name", Title: "Улица", Kind: "text"},
			{Name: "maxweight", Title: "Масса", Kind: "number", Unit: "т"},
			{Name: "maxaxleload", Title: "Нагрузка на ось", Kind: "number", Unit: "т"},
			{Name: "maxheight", Title: "Высота", Kind: "number", Unit: "м"},
			{Name: "maxwidth", Title: "Ширина", Kind: "number", Unit: "м"},
			{Name: "conditions", Title: "Условия", Kind: "text"},
			{Name: "signed", Title: "Подтверждено знаком", Kind: "bool"},
			{Name: "origins", Title: "Источники", Kind: "text"},
		},
	},
	{
		Code: "cargo_frame", Group: "network", Title: "Грузовой каркас",
		Description: "Улицы, по которым грузовикам более 2,5 т разрешено движение внутри МКАД, — по перечню Дептранса", Geometry: "line",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"regulation", "network"},
		Attribution: cargoFrameAttribution,
		Style: Style{
			Kind: "single", Color: "cat3", DashedBy: "partial",
			Note: "Пунктир — в перечне указан участок улицы, отмечена вся улица в округе",
		},
		Properties: []Property{
			{Name: "entry", Title: "Строка перечня", Kind: "text"},
			{Name: "name", Title: "Улица в OSM", Kind: "text"},
			{Name: "okrug", Title: "Округ", Kind: "text"},
			{Name: "partial", Title: "Участок улицы", Kind: "bool"},
		},
	},
	{
		Code: "restriction_conflicts", Group: "network", Title: "Расхождения знаков и OSM",
		Description: "Участки, где знак Москвы и разметка OSM расходятся", Geometry: "line",
		MinZoom: 11, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: mixedAttribution,
		Style: Style{Kind: "category", Property: "type", Categories: []Category{
			{Values: []string{"sign_not_in_osm"}, Label: "Знак есть, в OSM нет", Color: "cat4"},
			{Values: []string{"osm_not_signed"}, Label: "В OSM есть, знака рядом нет", Color: "cat1"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Улица", Kind: "text"},
			{Name: "type", Title: "Расхождение", Kind: "enum", Labels: map[string]string{
				"sign_not_in_osm": "знак Москвы без разметки OSM", "osm_not_signed": "разметка OSM без знака рядом",
			}},
			{Name: "kinds", Title: "Виды ограничений", Kind: "text"},
		},
	},
	{
		Code: "edge_centrality", Group: "network", Title: "Значимость участков",
		Description: "Доля кратчайших по времени путей грузовика 12 т между узлами сети, проходящих через участок; " +
			"верхние 20 % участков (на мелких масштабах — 5 %)", Geometry: "line",
		MinZoom: 9, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network_analysis"},
		Attribution: mixedAttribution,
		Style: Style{Kind: "category", Property: "level", Categories: []Category{
			{Values: []string{"top1"}, Label: "Верхний 1 %", Color: "crit"},
			{Values: []string{"top5"}, Label: "1–5 %", Color: "warn"},
			{Values: []string{"top20"}, Label: "5–20 %", Color: "cat1"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Улица", Kind: "text"},
			{Name: "rank", Title: "Место в рейтинге", Kind: "number"},
			{Name: "share", Title: "Доля путей через участок", Kind: "number", Unit: "%"},
			{Name: "level", Title: "Группа", Kind: "enum", Labels: map[string]string{
				"top1": "верхний 1 % участков", "top5": "верхние 1–5 %", "top20": "верхние 5–20 %",
			}},
		},
	},
	{
		Code: "bottlenecks", Group: "network", Title: "Узкие места по габаритам",
		Description: "Участки, закрытые грузовику 12 т пределом массы или габарита, через которые без ограничения " +
			"шли бы кратчайшие пути; цвет — значимость этих путей", Geometry: "line",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network_analysis"},
		Attribution: mixedAttribution,
		Style: Style{
			Kind: "category", Property: "importance", DashedBy: "no_detour",
			Note: "Пунктир — объезда для грузовика нет",
			Categories: []Category{
				{Values: []string{"high"}, Label: "Путь из верхних 5 %", Color: "crit"},
				{Values: []string{"medium"}, Label: "5–20 %", Color: "warn"},
				{Values: []string{"low"}, Label: "Прочие пути", Color: "n9"},
			},
		},
		Properties: []Property{
			{Name: "name", Title: "Улица", Kind: "text"},
			{Name: "kind", Title: "Ограничение", Kind: "enum", Labels: limitKinds},
			{Name: "value", Title: "Предел (т или м)", Kind: "number"},
			{Name: "detour_min", Title: "Дополнительное время объезда", Kind: "number", Unit: "мин"},
			{Name: "no_detour", Title: "Объезда нет", Kind: "bool"},
			{Name: "free_rank", Title: "Место без ограничения в рейтинге значимости", Kind: "number"},
		},
	},
	{
		Code: "critical_elements", Group: "network", Title: "Критичные мосты и участки",
		Description: "Рост времени доступа объектов реестра к МКАД для грузовика 12 т при закрытии моста или участка; " +
			"цвет — место в рейтинге", Geometry: "line",
		MinZoom: 9, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network_analysis"},
		Attribution: mixedAttribution,
		Style: Style{Kind: "category", Property: "level", Categories: []Category{
			{Values: []string{"top10"}, Label: "Первые 10", Color: "crit"},
			{Values: []string{"top50"}, Label: "11–50", Color: "warn"},
			{Values: []string{"other"}, Label: "Прочие с ростом времени", Color: "n9"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "kind", Title: "Элемент", Kind: "enum", Labels: map[string]string{
				"bridge": "мост, путепровод", "segment": "участок дороги",
			}},
			{Name: "rank", Title: "Место в рейтинге", Kind: "number"},
			{Name: "facilities", Title: "Объектов с ростом времени", Kind: "number"},
			{Name: "damage_min", Title: "Суммарный рост времени", Kind: "number", Unit: "объекто-мин"},
			{Name: "max_delay_min", Title: "Наибольший рост", Kind: "number", Unit: "мин"},
			{Name: "disconnected", Title: "Объектов без доступа к МКАД", Kind: "number"},
			{Name: "confirmed", Title: "Проверено матрицами Valhalla", Kind: "bool"},
			{Name: "valhalla_damage_min", Title: "Рост по Valhalla", Kind: "number", Unit: "объекто-мин"},
		},
	},
	{
		Code: "road_works", Group: "operations", Title: "Дорожные работы",
		Description: "Плановый и аварийный ремонт, ограничения движения на период работ — на дату шкалы", Geometry: "line",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"operations"},
		Attribution: mosAttribution, Visible: true,
		Style: Style{
			Kind: "category", Property: "significance",
			Note: "Значимость — по классам затронутых улиц, мостам и перекрытию",
			Categories: []Category{
				{Values: []string{"high"}, Label: "Высокая значимость", Color: "crit"},
				{Values: []string{"medium"}, Label: "Средняя", Color: "warn"},
				{Values: []string{"low"}, Label: "Низкая", Color: "info"},
			},
		},
		Properties: []Property{
			{Name: "title", Title: "Работы", Kind: "text"},
			{Name: "kind", Title: "Вид", Kind: "enum", Labels: map[string]string{
				"repair": "Плановый ремонт", "emergency": "Аварийный ремонт", "closure": "Ограничение движения",
			}},
			{Name: "place", Title: "Место", Kind: "text"},
			{Name: "state", Title: "Состояние сегодня", Kind: "enum", Labels: map[string]string{
				"planned": "запланированы", "active": "идут", "finished": "завершены",
			}},
			{Name: "starts", Title: "Начало", Kind: "text"},
			{Name: "ends", Title: "Окончание", Kind: "text"},
			{Name: "closure", Title: "Перекрытие проезжей части", Kind: "bool"},
			{Name: "source_status", Title: "Статус по данным источника", Kind: "text"},
			{Name: "customer", Title: "Заказчик", Kind: "text"},
			{Name: "contractor", Title: "Исполнитель", Kind: "text"},
		},
	},
	{
		Code: "incidents", Group: "operations", Title: "ДТП с пострадавшими",
		Description: "ДТП за последние три года и текущий по данным ГИБДД; цвет — тяжесть", Geometry: "point",
		MinZoom: 12, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"operations"},
		Attribution: dtpAttribution,
		Style: Style{Kind: "category", Property: "severity", Categories: []Category{
			{Values: []string{"fatal"}, Label: "С погибшими", Color: "crit"},
			{Values: []string{"severe"}, Label: "Тяжёлый вред здоровью", Color: "warn"},
			{Values: []string{"light", "none"}, Label: "Лёгкий вред здоровью", Color: "n9"},
		}},
		Properties: incidentProps,
	},
	{
		Code: "freight_incidents", Group: "operations", Title: "ДТП с грузовым транспортом",
		Description: "ДТП с участием грузовиков, тягачей, самосвалов, прицепов и фургонов за последние три года", Geometry: "point",
		MinZoom: 10, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"operations"},
		Attribution: dtpAttribution,
		Style: Style{Kind: "category", Property: "heavy_freight", Categories: []Category{
			{Values: []string{"true"}, Label: "Грузовик, тягач, самосвал, прицеп", Color: "cat2"},
			{Values: []string{"false"}, Label: "Грузовой фургон", Color: "cat5"},
		}},
		Properties: incidentProps,
	},
	{
		Code: "commissioning", Group: "objects", Title: "Ввод объектов в эксплуатацию",
		Description: "Разрешения на ввод (место — по адресному реестру); выделены склады и логистика", Geometry: "point",
		MinZoom: 10, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"facility"},
		Attribution: mosAttribution,
		Style: Style{Kind: "category", Property: "logistics", Categories: []Category{
			{Values: []string{"true"}, Label: "Склад, логистика", Color: "cat2"},
			{Values: []string{"false"}, Label: "Прочие объекты", Color: "n9"},
		}},
		Properties: []Property{
			{Name: "name", Title: "Объект", Kind: "text"},
			{Name: "purpose", Title: "Назначение", Kind: "text"},
			{Name: "address", Title: "Адрес", Kind: "text"},
			{Name: "permit_date", Title: "Дата разрешения", Kind: "text"},
			{Name: "area", Title: "Общая площадь", Kind: "number", Unit: "м²"},
		},
	},
	{
		Code: "facility_areas", Group: "objects", Title: "Контуры объектов",
		Description: "Территории и здания объектов логистики", Geometry: "polygon",
		MinZoom: 12, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"facility"},
		Attribution: osmAttribution, Visible: true,
		Style: Style{Kind: "single", Color: "acc"},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "type", Title: "Тип", Kind: "enum", Labels: facilityTypes},
		},
	},
	{
		Code: "freight_points", Group: "infra", Title: "Грузовые пункты",
		Description: "Ж/д грузовые станции и дворы, таможня, порты", Geometry: "point",
		MinZoom: 9, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: osmAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"rail_freight"}, Label: "Ж/д грузовой пункт", Color: "cat3"},
			{Values: []string{"customs"}, Label: "Таможня", Color: "cat4"},
			{Values: []string{"port"}, Label: "Порт", Color: "cat7"},
		}},
		Properties: poiProps,
	},
	{
		Code: "truck_parking", Group: "infra", Title: "Стоянки грузовиков",
		Description: "Стоянки для большегрузов и площадки отдыха водителей: реестры Москвы и OSM", Geometry: "point",
		MinZoom: 9, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: mixedAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"truck_parking"}, Label: "Стоянка грузовиков", Color: "cat2"},
			{Values: []string{"rest_area"}, Label: "Площадка отдыха", Color: "cat6"},
		}},
		Properties: poiProps,
	},
	{
		Code: "fuel_stations", Group: "infra", Title: "АЗС",
		Description: "Автозаправочные станции: реестр Москвы и OSM", Geometry: "point",
		MinZoom: 11, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: mixedAttribution,
		Style:       Style{Kind: "single", Color: "cat1"},
		Properties:  poiProps,
	},
	{
		Code: "attractors", Group: "infra", Title: "Точки притяжения",
		Description: "Рынки, ярмарки, вокзалы, ТПУ, технопарки — места притяжения грузопотоков", Geometry: "point",
		MinZoom: 10, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: mixedAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"marketplace", "fair"}, Label: "Рынок, ярмарка", Color: "cat5"},
			{Values: []string{"rail_station"}, Label: "Вокзал", Color: "cat8"},
			{Values: []string{"tpu"}, Label: "ТПУ", Color: "cat6"},
			{Values: []string{"technopark"}, Label: "Технопарк", Color: "cat4"},
		}},
		Properties: poiProps,
	},
	{
		Code: "facilities", Group: "objects", Title: "Объекты логистики",
		Description: "Склады, логистические и распределительные центры, терминалы", Geometry: "point",
		MinZoom: 8, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"facility", "accessibility"},
		Attribution: osmAttribution, Visible: true,
		Style: Style{
			Kind: "single", Color: "acc",
			Note: "Размер — площадь застройки; бледнее — достоверность ниже 0,5; на мелких масштабах — группы с числом объектов",
		},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "type", Title: "Тип", Kind: "enum", Labels: facilityTypes},
			{Name: "address", Title: "Адрес", Kind: "text"},
			{Name: "area", Title: "Площадь застройки", Kind: "number", Unit: "м²"},
			{Name: "buildings", Title: "Зданий", Kind: "number"},
			{Name: "confidence", Title: "Достоверность", Kind: "number"},
			{Name: "mkad_min", Title: "До МКАД (грузовик 20 т)", Kind: "number", Unit: "мин"},
			{Name: "ttk_min", Title: "До ТТК (грузовик 20 т)", Kind: "number", Unit: "мин"},
			{Name: "center_min", Title: "До центра (грузовик 20 т)", Kind: "number", Unit: "мин"},
			{Name: "rail_min", Title: "До ж/д грузового пункта (грузовик 20 т)", Kind: "number", Unit: "мин"},
		},
	},
}

// layerByCode — слой реестра по коду.
func layerByCode(code string) (Layer, bool) {
	for _, l := range Registry {
		if l.Code == code {
			return l, true
		}
	}
	return Layer{}, false
}
