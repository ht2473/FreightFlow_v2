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
	{"territory", "Территории"},
}

const osmAttribution = "© участники OpenStreetMap"

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
	"marketplace":   "Рынок, ярмарка",
	"rail_station":  "Вокзал",
	"rail_freight":  "Ж/д грузовой пункт",
	"customs":       "Таможня",
	"port":          "Порт",
}

var poiProps = []Property{
	{Name: "name", Title: "Название", Kind: "text"},
	{Name: "kind", Title: "Вид", Kind: "enum", Labels: poiKinds},
	{Name: "operator", Title: "Оператор", Kind: "text"},
	{Name: "opening_hours", Title: "Часы работы", Kind: "text"},
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
		Code: "restrictions", Group: "network", Title: "Ограничения для грузовиков",
		Description: "Запреты и пределы массы и габаритов по тегам OSM", Geometry: "line",
		MinZoom: 10, MaxZoom: 15, Permission: identity.PermMapView, Domains: []string{"network"},
		Attribution: osmAttribution, Visible: true,
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
			{Name: "conditions", Title: "Когда действует", Kind: "text"},
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
		Description: "Стоянки для грузовых автомобилей и площадки отдыха", Geometry: "point",
		MinZoom: 9, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: osmAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"truck_parking"}, Label: "Стоянка грузовиков", Color: "cat2"},
			{Values: []string{"rest_area"}, Label: "Площадка отдыха", Color: "cat6"},
		}},
		Properties: poiProps,
	},
	{
		Code: "fuel_stations", Group: "infra", Title: "АЗС",
		Description: "Автозаправочные станции", Geometry: "point",
		MinZoom: 11, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: osmAttribution,
		Style:       Style{Kind: "single", Color: "cat1"},
		Properties:  poiProps,
	},
	{
		Code: "attractors", Group: "infra", Title: "Рынки и вокзалы",
		Description: "Точки притяжения грузопотоков: рынки, ярмарки, вокзалы", Geometry: "point",
		MinZoom: 10, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"infra"},
		Attribution: osmAttribution,
		Style: Style{Kind: "category", Property: "kind", Categories: []Category{
			{Values: []string{"marketplace"}, Label: "Рынок, ярмарка", Color: "cat5"},
			{Values: []string{"rail_station"}, Label: "Вокзал", Color: "cat8"},
		}},
		Properties: poiProps,
	},
	{
		Code: "facilities", Group: "objects", Title: "Объекты логистики",
		Description: "Склады, логистические и распределительные центры, терминалы", Geometry: "point",
		MinZoom: 8, MaxZoom: 16, Permission: identity.PermMapView, Domains: []string{"facility"},
		Attribution: osmAttribution, Visible: true,
		Style: Style{
			Kind: "single", Color: "acc",
			Note: "Размер — площадь застройки; бледнее — достоверность ниже 0,5",
		},
		Properties: []Property{
			{Name: "name", Title: "Название", Kind: "text"},
			{Name: "type", Title: "Тип", Kind: "enum", Labels: facilityTypes},
			{Name: "area", Title: "Площадь застройки", Kind: "number", Unit: "м²"},
			{Name: "buildings", Title: "Зданий", Kind: "number"},
			{Name: "confidence", Title: "Достоверность", Kind: "number"},
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
