#!/bin/bash
# Сборка графа Valhalla из /custom_files/routing.osm.pbf (выгрузка с ограничениями из знаков,
# freightflow routing prepare) в /custom_files/next; после проверки next становится current,
# прежний граф сохраняется в prev. Служба читает /custom_files/current/valhalla.json.
set -euo pipefail
cd /custom_files

pbf=routing.osm.pbf
test -s "$pbf" || { echo "нет $pbf: сначала task routing:prepare" >&2; exit 1; }

rm -rf next
mkdir -p next

# Исключаемые многоугольники — кольца зон (периметр МКАД ~110 км) и перекрытия сценариев:
# предел их общей длины поднят с 10 км по умолчанию.
config() {
	valhalla_build_config \
		--mjolnir-tile-dir "/custom_files/$1/tiles" \
		--mjolnir-tile-extract "/custom_files/$1/tiles.tar" \
		--mjolnir-admin "/custom_files/$1/admins.sqlite" \
		--mjolnir-concurrency "$(nproc)" \
		--service-limits-max-exclude-polygons-length 2000000 \
		--service-limits-max-exclude-locations 200
}
config next >next/build.json

valhalla_build_admins -c next/build.json "$pbf"
valhalla_build_tiles -c next/build.json "$pbf"
valhalla_build_extract -c next/build.json -v --overwrite

test -s next/tiles.tar || { echo "граф не собран: нет tiles.tar" >&2; exit 1; }
# Тайлы упакованы в tiles.tar — каталог отдельных файлов службе не нужен.
rm -rf next/tiles

config current >next/valhalla.json
date -u +%Y-%m-%dT%H:%M:%SZ >next/built_at
sha256sum "$pbf" | cut -c1-16 >next/source

rm -rf prev
if [ -d current ]; then mv current prev; fi
mv next current
echo "граф собран: $(du -sh current | cut -f1)"
