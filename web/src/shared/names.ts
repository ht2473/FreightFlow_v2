/** Названия ролей для интерфейса (коды — internal/identity/permission.go). */
export const roleTitles: Record<string, string> = {
  admin: "Администратор",
  data_steward: "Редактор данных",
  analyst: "Аналитик",
  operator: "Оператор мониторинга",
  viewer: "Наблюдатель",
};

export function roleTitle(code: string): string {
  return roleTitles[code] ?? code;
}

/** Действия журнала аудита. */
const actionTitles: Record<string, string> = {
  "auth.login": "Вход",
  "auth.login_failed": "Неудачный вход",
  "auth.password_changed": "Смена пароля",
  "auth.password_reset": "Восстановление пароля",
  "auth.password_reset_requested": "Запрос восстановления пароля",
  "auth.totp_enabled": "Включена 2FA",
  "auth.totp_disabled": "Отключена 2FA",
  "token.create": "Выпуск токена API",
  "token.revoke": "Отзыв токена API",
  "user.create": "Создание пользователя",
  "user.invite": "Приглашение",
  "user.invite_accepted": "Принято приглашение",
  "user.set_roles": "Изменение ролей",
  "user.set_status": "Изменение статуса",
  "user.update_profile": "Изменение профиля",
  "data.run_started": "Запуск загрузки",
  "data.source_updated": "Настройка источника",
};

export function actionTitle(code: string): string {
  return actionTitles[code] ?? code;
}

export const auditActions = Object.keys(actionTitles);
