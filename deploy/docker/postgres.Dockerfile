# PostgreSQL 18 с PostGIS 3.6 и H3 для FreightFlow.
FROM postgis/postgis:18-3.6

RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-18-h3 \
    && rm -rf /var/lib/apt/lists/*
