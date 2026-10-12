# Процесс фоновых задач с инструментами обработки OpenStreetMap, подложки и геоданных.
# Бинарник собирается заранее: task build:linux (bin/linux/freightflow).

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        osm2pgsql osmium-tool gdal-bin ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

# pmtiles — вырезка подложки из сборок Protomaps (в Debian пакета нет).
ARG PMTILES_VERSION=1.31.2
ADD --checksum=sha256:3ed7dbf4ec2e6dfe5e25b6f70d1ffc932729f93c86db353bf514dd71010a312f \
    https://github.com/protomaps/go-pmtiles/releases/download/v${PMTILES_VERSION}/go-pmtiles_${PMTILES_VERSION}_Linux_x86_64.tar.gz \
    /tmp/pmtiles.tar.gz
RUN tar -xzf /tmp/pmtiles.tar.gz -C /usr/local/bin pmtiles && rm /tmp/pmtiles.tar.gz

RUN useradd --system --uid 10001 --home-dir /app freightflow \
    && mkdir -p /app /data \
    && chown freightflow /data

COPY bin/linux/freightflow /usr/local/bin/freightflow

ENV FF_DATA_DIR=/data \
    TZ=Europe/Moscow

USER freightflow
WORKDIR /app
ENTRYPOINT ["freightflow"]
CMD ["worker"]
