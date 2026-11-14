-- Очистка базы, перенесённой с машины разработки: учётные записи и всё, что им принадлежит
-- (виды, дашборды, сценарии, оценки локации, правила алертов, отчёты, токены, уведомления),
-- удаляются каскадом; правки реестра и история загрузок остаются без автора.
BEGIN;
DELETE FROM core.app_user;
TRUNCATE core.session, core.audit_log, core.one_time_token;
TRUNCATE alerting.alert, alerting.hit, alerting.alert_log CASCADE;
TRUNCATE notify.notification, notify.delivery, notify.outbox CASCADE;
COMMIT;
ANALYZE;
