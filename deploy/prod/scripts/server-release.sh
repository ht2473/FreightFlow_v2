#!/bin/bash
# Серверная часть выкладки (вызывает release.sh): образы, миграции, перезапуск.
#   bash server-release.sh <версия>     — выкладка собранной версии
#   bash server-release.sh rollback     — возврат к предыдущему образу (без отката миграций)
set -euo pipefail
cd /srv/freightflow
VERSION=${1:?версия или rollback}

test -s .env || { echo "нет /srv/freightflow/.env (образец — deploy/prod/env.example)" >&2; exit 1; }
chmod 600 .env
chmod +x backup.sh

if [ "$VERSION" = "rollback" ]; then
	docker image inspect freightflow/app:previous >/dev/null
	docker tag freightflow/app:previous freightflow/app:current
	docker compose up -d app worker
	echo "откат выполнен: $(cat RELEASE.previous 2>/dev/null || echo 'предыдущая версия')"
	exit 0
fi

docker build -q -f build/worker.Dockerfile -t "freightflow/app:$VERSION" build >/dev/null
if docker image inspect freightflow/app:current >/dev/null 2>&1; then
	docker tag freightflow/app:current freightflow/app:previous
	cp -f RELEASE RELEASE.previous 2>/dev/null || true
fi
docker tag "freightflow/app:$VERSION" freightflow/app:current

docker compose build -q db
docker compose up -d --wait db
docker compose run --rm --no-deps app migrate up
docker compose up -d --remove-orphans
docker image prune -f >/dev/null
echo "$VERSION" >RELEASE
docker compose ps --format 'table {{.Service}}\t{{.Status}}'
