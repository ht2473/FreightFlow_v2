#!/bin/bash
# Серверная часть переноса данных (вызывает push-data.sh): база из transfer/freightflow.dump.
# Также восстанавливает резервную копию: bash server-restore.sh backups/freightflow-....dump
set -euo pipefail
cd /srv/freightflow
DUMP=${1:-transfer/freightflow.dump}
test -s "$DUMP" || { echo "нет файла $DUMP" >&2; exit 1; }

docker compose stop app worker
docker compose up -d --wait db
docker compose exec -T db dropdb -U freightflow --if-exists --force freightflow
docker compose exec -T db createdb -U freightflow freightflow
docker compose exec -T db pg_restore -U freightflow -d freightflow --no-owner --exit-on-error <"$DUMP"
if [ "$DUMP" = transfer/freightflow.dump ]; then
	docker compose exec -T db psql -U freightflow -d freightflow -v ON_ERROR_STOP=1 -q <transfer/cleanup.sql
	rm -f "$DUMP"
fi
# Каталог данных доступен пользователю контейнеров.
sudo -n chown -R 10001 var 2>/dev/null || docker run --rm -v "$PWD/var:/v" alpine:3.22 chown -R 10001 /v
docker compose run --rm --no-deps app migrate up
docker compose up -d
docker compose ps --format 'table {{.Service}}\t{{.Status}}'
