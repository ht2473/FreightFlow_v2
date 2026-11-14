#!/bin/bash
# Перенос данных с машины разработки на сервер (Git Bash):
#   deploy/prod/scripts/push-data.sh deploy@<адрес сервера>
# База (без сессий, журнала аудита, очередей и уведомлений) восстанавливается на сервере,
# учётные записи удаляются (cleanup.sql); из var переносятся подложка, граф маршрутов,
# сертификаты и снимки источников (без снимков OSM).
# ВНИМАНИЕ: база на сервере заменяется целиком, включая созданных там пользователей.
set -euo pipefail
HOST=${1:?укажите сервер: deploy@адрес}
cd "$(git rev-parse --show-toplevel)"
DEV="docker compose -f deploy/compose.dev.yaml"

read -r -p "База на $HOST будет заменена данными с этой машины. Продолжить? [да/нет] " ok
[ "$ok" = "да" ] || exit 1

mkdir -p tmp/transfer
echo "== дамп базы"
$DEV exec -T db pg_dump -U freightflow -Fc -Z 6 \
	--exclude-table-data=core.session \
	--exclude-table-data=core.audit_log \
	--exclude-table-data=queue.river_job \
	--exclude-table-data=notify.outbox \
	--exclude-table-data=notify.delivery \
	--exclude-table-data=notify.notification \
	freightflow >tmp/transfer/freightflow.dump
du -h tmp/transfer/freightflow.dump

echo "== копирование дампа"
scp tmp/transfer/freightflow.dump deploy/prod/scripts/cleanup.sql deploy/prod/scripts/server-restore.sh \
	"$HOST:/srv/freightflow/transfer/"

echo "== копирование данных var"
tar -C var -czf - \
	--exclude='raw/osm' --exclude='routing/prev' --exclude='routing/next' --exclude='routing/routing.osm.pbf' \
	basemap certs routing raw | ssh "$HOST" 'tar -C /srv/freightflow/var -xzf - --no-same-owner'

echo "== восстановление базы"
ssh "$HOST" 'bash /srv/freightflow/transfer/server-restore.sh'
rm -f tmp/transfer/freightflow.dump
echo "== готово. Учётных записей на сервере нет: создайте администратора (deploy/prod/README.md)"
