-- Очистка базы, перенесённой с машины разработки: учётные записи и всё, что им принадлежит
-- (виды, дашборды, сценарии, оценки локации, правила алертов, отчёты, токены, уведомления),
-- удаляются каскадом; правки реестра и история загрузок остаются без автора.
BEGIN;
DELETE FROM core.app_user;
TRUNCATE core.session, core.audit_log, core.one_time_token;
TRUNCATE alerting.alert, alerting.hit, alerting.alert_log CASCADE;
TRUNCATE notify.notification, notify.delivery, notify.outbox CASCADE;
-- Сервер вне РФ (Р-66): российские источники ему не отвечают, их данные приходят переносом
-- с машины разработки.
UPDATE ingest.source SET enabled = false WHERE ru_network;
COMMIT;
ANALYZE;
