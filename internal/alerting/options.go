package alerting

// Option — значение справочника конструктора правил.
type Option struct {
	Code  string
	Title string
}

// KindOption — вид правила с допустимыми способами задания области.
type KindOption struct {
	Code        string
	Title       string
	Description string
	Scopes      []string
}

// Options — справочники конструктора правил, не зависящие от данных.
type Options struct {
	Kinds            []KindOption
	WorkKinds        []Option
	Changes          []Option
	RestrictionKinds []Option
	Targets          []Option
	FacilityTypes    []Option
}

var kindTitles = map[string][2]string{
	KindRoadworks:     {"Дорожные работы", "Новые и изменённые работы на коридоре, у объектов или в области"},
	KindRestrictions:  {"Ограничения", "Новые, изменённые и снятые ограничения для грузовиков в области"},
	KindAccessibility: {"Доступность", "Рост времени до МКАД, ТТК, центра или ж/д пункта у выбранных объектов"},
	KindFacilities:    {"Новые объекты", "Объекты, впервые появившиеся в реестре"},
	KindSources:       {"Источники данных", "Сбой загрузки и данные, не обновлявшиеся дольше порога"},
}

// Catalog возвращает справочники в порядке показа.
func Catalog() Options {
	var o Options
	for _, k := range Kinds {
		o.Kinds = append(o.Kinds, KindOption{Code: k, Title: kindTitles[k][0], Description: kindTitles[k][1], Scopes: kindScopes[k]})
	}
	for _, k := range workKinds {
		o.WorkKinds = append(o.WorkKinds, Option{k, map[string]string{"repair": "Плановые", "emergency": "Аварийные", "closure": "Перекрытия"}[k]})
	}
	for _, c := range workChanges {
		o.Changes = append(o.Changes, Option{c, map[string]string{"appeared": "Появилась", "changed": "Изменилась", "started": "Началась"}[c]})
	}
	for _, k := range restrictionKinds {
		o.RestrictionKinds = append(o.RestrictionKinds, Option{k, restrictionTitles[k]})
	}
	for _, t := range accessTargets {
		o.Targets = append(o.Targets, Option{t, map[string]string{"mkad": "МКАД", "ttk": "ТТК", "center": "Центр", "rail_freight": "Ж/д пункт"}[t]})
	}
	for _, t := range facilityTypes {
		o.FacilityTypes = append(o.FacilityTypes, Option{t, facilityTitles[t]})
	}
	return o
}
