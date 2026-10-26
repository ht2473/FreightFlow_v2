-- +goose Up

-- Земельный покров из OSM для исключающих зон оценки локации: жилая застройка, зелёные зоны (парки,
-- заповедники, охраняемые территории), водные объекты. Контуры от 2000 м².
CREATE TABLE territory.land_cover (
    id          uuid PRIMARY KEY,
    kind        text NOT NULL,
    name        text,
    geom        geometry(MultiPolygon, 4326) NOT NULL,
    area_m2     double precision NOT NULL,
    osm_type    char(1) NOT NULL,
    osm_id      bigint NOT NULL,
    source_code text NOT NULL,
    snapshot_id uuid,
    CONSTRAINT land_cover_kind_chk CHECK (kind IN ('residential', 'green', 'water'))
);

CREATE INDEX land_cover_geom_idx ON territory.land_cover USING gist (geom);
CREATE INDEX land_cover_kind_idx ON territory.land_cover (kind);

-- +goose Down
DROP TABLE territory.land_cover;
