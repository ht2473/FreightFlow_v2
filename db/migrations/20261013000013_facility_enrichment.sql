-- Обогащение реестра объектов официальными записями: адрес из адресного реестра Москвы, связь с
-- разрешениями на ввод, пересчёт достоверности.

-- +goose Up

-- Связи объекта с записями других источников (сам объект — из OSM).
CREATE TABLE infra.facility_source (
    facility_id uuid NOT NULL REFERENCES infra.facility (id) ON DELETE CASCADE,
    relation    text NOT NULL,
    ref         text NOT NULL,
    details     jsonb NOT NULL DEFAULT '{}'::jsonb,
    matched_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (facility_id, relation, ref),
    CONSTRAINT facility_source_relation_chk CHECK (relation IN ('address', 'permit'))
);

COMMENT ON TABLE infra.facility_source IS
    'address — объект адресного реестра (ref — UNOM); permit — разрешение на ввод (ref — global_id набора 64028)';

CREATE INDEX facility_source_ref_idx ON infra.facility_source (relation, ref);

ALTER TABLE infra.facility ADD COLUMN address_unom bigint, ADD COLUMN official_address text;

COMMENT ON COLUMN infra.facility.official_address IS 'адрес по адресному реестру Москвы; address — из разметки OSM';

-- Адрес объекта: объект адресного реестра с наибольшим пересечением с контуром зданий (или
-- содержащий точку объекта), иначе ближайшая точка реестра не дальше 50 м. Разрешение на ввод
-- складского назначения, привязанное к объекту, и адрес из реестра добавляют причины
-- достоверности; прежние причины этих видов заменяются. Возвращает число объектов с адресом
-- реестра и с разрешениями.
-- +goose StatementBegin
CREATE FUNCTION infra.enrich_facilities(OUT addressed integer, OUT permitted integer)
    LANGUAGE plpgsql AS $$
BEGIN
    UPDATE infra.facility f SET address_unom = x.unom, official_address = x.addr
    FROM (
        SELECT f2.id, a.unom, coalesce(a.simple_address, a.address) AS addr
        FROM infra.facility f2
        LEFT JOIN LATERAL (
            SELECT coalesce(
                (SELECT a1.unom FROM territory.address a1
                 WHERE a1.geom && coalesce(f2.footprint, f2.territory, f2.point)
                   AND ST_Intersects(a1.geom, coalesce(f2.footprint, f2.point))
                 ORDER BY CASE WHEN f2.footprint IS NOT NULL
                               THEN ST_Area(ST_Intersection(ST_MakeValid(a1.geom), f2.footprint)) ELSE 0 END DESC,
                          a1.point <-> f2.point
                 LIMIT 1),
                (SELECT a2.unom FROM territory.address a2
                 WHERE a2.point IS NOT NULL AND ST_DWithin(a2.point::geography, f2.point::geography, 50)
                 ORDER BY a2.point <-> f2.point
                 LIMIT 1)) AS unom
        ) u ON true
        LEFT JOIN territory.address a ON a.unom = u.unom
        WHERE f2.status = 'active'
    ) x
    WHERE f.id = x.id AND (f.address_unom IS DISTINCT FROM x.unom OR f.official_address IS DISTINCT FROM x.addr);

    DELETE FROM infra.facility_source WHERE relation = 'address';
    INSERT INTO infra.facility_source (facility_id, relation, ref, details)
    SELECT f.id, 'address', f.address_unom::text, jsonb_build_object('address', a.simple_address, 'obj_type', a.obj_type)
    FROM infra.facility f JOIN territory.address a ON a.unom = f.address_unom
    WHERE f.status = 'active';

    DELETE FROM infra.facility_source WHERE relation = 'permit';
    INSERT INTO infra.facility_source (facility_id, relation, ref, details)
    SELECT c.facility_id, 'permit', c.mos_global_id::text,
           jsonb_strip_nulls(jsonb_build_object('number', c.permit_number, 'date', c.permit_date, 'name', c.object_name,
                                                'logistics', c.logistics))
    FROM infra.commissioning c
    WHERE c.facility_id IS NOT NULL;

    UPDATE infra.facility f SET confidence_reasons = r.reasons,
        confidence = least(1.0, (SELECT coalesce(sum((e->>'weight')::numeric), 0) FROM jsonb_array_elements(r.reasons) e))::real
    FROM (
        SELECT f2.id,
               coalesce((SELECT jsonb_agg(e) FROM jsonb_array_elements(f2.confidence_reasons) e
                         WHERE e->>'code' NOT IN ('permit', 'registry')), '[]'::jsonb)
               || CASE WHEN EXISTS (SELECT 1 FROM infra.facility_source s
                                    WHERE s.facility_id = f2.id AND s.relation = 'permit' AND (s.details->>'logistics')::boolean)
                       THEN jsonb_build_array(jsonb_build_object('code', 'permit', 'weight', 0.25,
                            'text', 'Разрешение на ввод объекта складского или логистического назначения'))
                       ELSE '[]'::jsonb END
               || CASE WHEN f2.address_unom IS NOT NULL
                       THEN jsonb_build_array(jsonb_build_object('code', 'registry', 'weight', 0.05,
                            'text', 'Адрес подтверждён адресным реестром Москвы'))
                       ELSE '[]'::jsonb END AS reasons
        FROM infra.facility f2
        WHERE f2.status = 'active'
    ) r
    WHERE f.id = r.id AND f.confidence_reasons IS DISTINCT FROM r.reasons;

    SELECT count(*) FILTER (WHERE address_unom IS NOT NULL),
           count(*) FILTER (WHERE EXISTS (SELECT 1 FROM infra.facility_source s WHERE s.facility_id = f.id AND s.relation = 'permit'))
    INTO addressed, permitted
    FROM infra.facility f WHERE f.status = 'active';
END
$$;
-- +goose StatementEnd

-- Тайлы объектов: адрес — по реестру, иначе из OSM.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.facilities(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    -- Крупные снизу, мелкие поверх.
    SELECT coalesce(ST_AsMVT(t, 'facilities', 4096, 'geom', 'fid' ORDER BY t.area DESC NULLS LAST), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type,
               f.name, coalesce(f.official_address, f.address) AS address,
               round(f.confidence::numeric, 2)::float8 AS confidence,
               f.building_area_m2::float8 AS area, f.buildings,
               ST_AsMVTGeom(f.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.facility f
        WHERE f.status = 'active' AND f.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tiles.facilities(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    -- Крупные снизу, мелкие поверх.
    SELECT coalesce(ST_AsMVT(t, 'facilities', 4096, 'geom', 'fid' ORDER BY t.area DESC NULLS LAST), '')
    FROM (
        SELECT tiles.osm_fid(f.osm_type, f.osm_id) AS fid, f.id::text AS id, f.type_code AS type,
               f.name, round(f.confidence::numeric, 2)::float8 AS confidence,
               f.building_area_m2::float8 AS area, f.buildings,
               ST_AsMVTGeom(f.point_3857, ST_TileEnvelope(z, x, y), 4096, 64, true) AS geom
        FROM infra.facility f
        WHERE f.status = 'active' AND f.point_3857 && ST_TileEnvelope(z, x, y, margin => 64.0 / 4096)
    ) t
$$;
-- +goose StatementEnd

DROP FUNCTION infra.enrich_facilities();
ALTER TABLE infra.facility DROP COLUMN official_address, DROP COLUMN address_unom;
DROP TABLE infra.facility_source;
