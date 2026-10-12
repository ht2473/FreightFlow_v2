package identity

import "slices"

// Permission — право на действие в системе. Права заданы в коде; роль — набор прав.
type Permission string

// Права системы.
const (
	PermMapView         Permission = "map.view"
	PermAnalyticsView   Permission = "analytics.view"
	PermReportsView     Permission = "reports.view"
	PermRoutingUse      Permission = "routing.use"
	PermViewsManage     Permission = "views.manage"
	PermExportsCreate   Permission = "exports.create"
	PermReportsCreate   Permission = "reports.create"
	PermScenariosManage Permission = "scenarios.manage"
	PermAlertsOwn       Permission = "alerts.own"
	PermAlertsManage    Permission = "alerts.manage"
	PermRegistryCurate  Permission = "registry.curate"
	PermDataManage      Permission = "data.manage"
	PermTokensOwn       Permission = "tokens.own"
	PermUsersManage     Permission = "users.manage"
	PermSettingsManage  Permission = "settings.manage"
	PermAuditView       Permission = "audit.view"
)

// PermissionInfo — описание права для интерфейса.
type PermissionInfo struct {
	Code  Permission
	Title string
}

// Permissions — все права с описаниями, в порядке отображения.
var Permissions = []PermissionInfo{
	{PermMapView, "Просмотр карты и реестра"},
	{PermAnalyticsView, "Просмотр аналитики"},
	{PermReportsView, "Просмотр отчётов"},
	{PermRoutingUse, "Маршруты и изохроны"},
	{PermViewsManage, "Сохранённые виды"},
	{PermExportsCreate, "Выгрузки данных"},
	{PermReportsCreate, "Формирование отчётов"},
	{PermScenariosManage, "Сценарии и оценка локации"},
	{PermAlertsOwn, "Собственные правила оповещений"},
	{PermAlertsManage, "Подтверждение и закрытие алертов"},
	{PermRegistryCurate, "Уточнение реестра объектов"},
	{PermDataManage, "Источники данных и загрузки"},
	{PermTokensOwn, "Собственные токены API"},
	{PermUsersManage, "Пользователи и роли"},
	{PermSettingsManage, "Настройки системы"},
	{PermAuditView, "Журнал аудита"},
}

// Коды встроенных ролей.
const (
	RoleViewer      = "viewer"
	RoleAnalyst     = "analyst"
	RoleOperator    = "operator"
	RoleDataSteward = "data_steward"
	RoleAdmin       = "admin"
)

// BuiltinRole — роль, поставляемая с системой.
type BuiltinRole struct {
	Code        string
	Name        string
	Description string
	Permissions []Permission
}

var (
	viewerPerms   = []Permission{PermMapView, PermAnalyticsView, PermReportsView, PermRoutingUse}
	analystPerms  = append(slices.Clone(viewerPerms), PermViewsManage, PermExportsCreate, PermReportsCreate, PermScenariosManage, PermAlertsOwn, PermTokensOwn)
	operatorPerms = append(slices.Clone(viewerPerms), PermViewsManage, PermExportsCreate, PermReportsCreate, PermAlertsOwn, PermAlertsManage, PermTokensOwn)
	stewardPerms  = append(slices.Clone(analystPerms), PermRegistryCurate, PermDataManage)
)

// BuiltinRoles — роли по умолчанию (01-product.md, раздел 3).
var BuiltinRoles = []BuiltinRole{
	{RoleViewer, "Наблюдатель", "Просмотр карты, дашбордов, реестра и отчётов", viewerPerms},
	{RoleAnalyst, "Аналитик", "Работа с данными: виды, выгрузки, отчёты, сценарии", analystPerms},
	{RoleOperator, "Оператор мониторинга", "Мониторинг и работа с алертами", operatorPerms},
	{RoleDataSteward, "Редактор данных", "Источники данных, качество, уточнение реестра", stewardPerms},
	{RoleAdmin, "Администратор", "Полный доступ, пользователи и настройки", allPermissions()},
}

func allPermissions() []Permission {
	out := make([]Permission, 0, len(Permissions))
	for _, p := range Permissions {
		out = append(out, p.Code)
	}
	return out
}

// KnownPermission сообщает, существует ли право с таким кодом.
func KnownPermission(code string) bool {
	return slices.ContainsFunc(Permissions, func(p PermissionInfo) bool { return string(p.Code) == code })
}
