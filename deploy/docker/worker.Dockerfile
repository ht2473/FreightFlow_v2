# Процесс фоновых задач с инструментами обработки OpenStreetMap и геоданных.
# Бинарник собирается заранее: task build:linux (bin/linux/freightflow).

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        osm2pgsql osmium-tool gdal-bin ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

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
