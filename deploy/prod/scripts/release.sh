#!/bin/bash
# Выкладка новой версии с машины разработки (Git Bash):
#   deploy/prod/scripts/release.sh deploy@<адрес сервера>
# Собирает фронтенд и бинарник под Linux, копирует их на сервер, где собираются образы,
# применяются миграции и перезапускаются службы (server-release.sh). Прежний образ
# остаётся как freightflow/app:previous для отката.
set -euo pipefail
HOST=${1:?укажите сервер: deploy@адрес}
cd "$(git rev-parse --show-toplevel)"

VERSION=$(git describe --tags --always --dirty)
case "$VERSION" in
*-dirty)
	echo "есть незакоммиченные изменения: выкладываются только коммиты" >&2
	exit 1
	;;
esac

echo "== сборка $VERSION"
(cd web && npm run build)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build -ldflags "-X freightflow/internal/app.Version=$VERSION" -o bin/linux/freightflow ./cmd/freightflow

echo "== копирование на $HOST"
ssh "$HOST" 'mkdir -p /srv/freightflow/build/bin/linux'
scp -q bin/linux/freightflow "$HOST:/srv/freightflow/build/bin/linux/freightflow"
scp -q deploy/docker/worker.Dockerfile deploy/docker/postgres.Dockerfile "$HOST:/srv/freightflow/build/"
scp -q deploy/prod/compose.yaml deploy/prod/Caddyfile \
	deploy/prod/scripts/backup.sh deploy/prod/scripts/server-release.sh "$HOST:/srv/freightflow/"

echo "== образы, миграции, перезапуск"
ssh "$HOST" "bash /srv/freightflow/server-release.sh '$VERSION'"
echo "== готово: $VERSION"
