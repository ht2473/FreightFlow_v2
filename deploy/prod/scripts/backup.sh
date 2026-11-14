#!/bin/bash
# Резервная копия базы в /srv/freightflow/backups, хранится KEEP_DAYS дней.
# Расписание (crontab -e пользователя deploy):
#   30 3 * * * /srv/freightflow/backup.sh >>/srv/freightflow/backups/backup.log 2>&1
set -euo pipefail
cd /srv/freightflow
KEEP_DAYS=${KEEP_DAYS:-7}

name="backups/freightflow-$(date +%Y%m%d-%H%M).dump"
docker compose exec -T db pg_dump -U freightflow -Fc -Z 6 \
	--exclude-table-data=core.session \
	--exclude-table-data=queue.river_job \
	freightflow >"$name.part"
mv "$name.part" "$name"
find backups -name 'freightflow-*.dump' -mtime +"$KEEP_DAYS" -delete
echo "$(date -Is) $name $(du -h "$name" | cut -f1)"
