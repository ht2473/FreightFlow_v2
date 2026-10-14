-- Уточнение реестра: правки редактора поверх источников (поле, значение, причина, автор) и
-- сообщения аналитиков о неточностях. Правки применяются после каждого переноса OSM и
-- обогащения — перезагрузка источника их не теряет.

-- +goose Up

CREATE TABLE infra.facility_curation (
    id          uuid PRIMARY KEY,
    facility_id uuid NOT NULL REFERENCES infra.facility (id) ON DELETE CASCADE,
    field       text NOT NULL,
    value       text,
    source_value text,
    reason      text NOT NULL,
    author_id   uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz,
    revoked_by  uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    CONSTRAINT facility_curation_field_chk CHECK (field IN ('name', 'type_code', 'status', 'address')),
    CONSTRAINT facility_curation_reason_chk CHECK (length(trim(reason)) > 0)
);

COMMENT ON TABLE infra.facility_curation IS 'правки реестра редактором; действует последняя неотменённая правка поля';
COMMENT ON COLUMN infra.facility_curation.source_value IS 'значение по источникам до первой правки поля — возвращается при отмене всех правок';

CREATE INDEX facility_curation_facility_idx ON infra.facility_curation (facility_id, created_at DESC);

CREATE TABLE infra.facility_report (
    id          uuid PRIMARY KEY,
    facility_id uuid NOT NULL REFERENCES infra.facility (id) ON DELETE CASCADE,
    author_id   uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    message     text NOT NULL,
    status      text NOT NULL DEFAULT 'open',
    resolution  text,
    resolved_by uuid REFERENCES core.app_user (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    CONSTRAINT facility_report_status_chk CHECK (status IN ('open', 'resolved', 'rejected')),
    CONSTRAINT facility_report_message_chk CHECK (length(trim(message)) > 0)
);

COMMENT ON TABLE infra.facility_report IS 'сообщения о неточностях записи реестра — очередь редактора';

CREATE INDEX facility_report_open_idx ON infra.facility_report (created_at) WHERE status = 'open';
CREATE INDEX facility_report_facility_idx ON infra.facility_report (facility_id, created_at DESC);

ALTER TABLE infra.facility ADD COLUMN curated text[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN infra.facility.curated IS 'поля, значения которых заданы редактором';

-- Действующие правки (последняя неотменённая по полю) — в записи объекта.
-- +goose StatementBegin
CREATE FUNCTION infra.apply_curation() RETURNS integer
    LANGUAGE plpgsql AS $$
DECLARE
    n integer;
BEGIN
    WITH cur AS (
        SELECT DISTINCT ON (facility_id, field) facility_id, field, value
        FROM infra.facility_curation
        WHERE revoked_at IS NULL
        ORDER BY facility_id, field, created_at DESC
    ), agg AS (
        SELECT f.id,
               (SELECT array_agg(c.field ORDER BY c.field) FROM cur c WHERE c.facility_id = f.id) AS fields,
               (SELECT c.value FROM cur c WHERE c.facility_id = f.id AND c.field = 'name') AS name,
               (SELECT c.value FROM cur c WHERE c.facility_id = f.id AND c.field = 'type_code') AS type_code,
               (SELECT c.value FROM cur c WHERE c.facility_id = f.id AND c.field = 'status') AS status,
               (SELECT c.value FROM cur c WHERE c.facility_id = f.id AND c.field = 'address') AS address
        FROM infra.facility f
        WHERE f.id IN (SELECT facility_id FROM cur) OR f.curated <> '{}'
    )
    UPDATE infra.facility f SET
        name = CASE WHEN 'name' = ANY (a.fields) THEN a.name ELSE f.name END,
        type_code = CASE WHEN 'type_code' = ANY (a.fields) THEN a.type_code ELSE f.type_code END,
        status = CASE WHEN 'status' = ANY (a.fields) THEN a.status
                      WHEN 'status' = ANY (f.curated) AND f.status = 'excluded' THEN 'active'
                      ELSE f.status END,
        official_address = CASE WHEN 'address' = ANY (a.fields) THEN a.address ELSE f.official_address END,
        curated = coalesce(a.fields, '{}'),
        updated_at = now()
    FROM agg a
    WHERE f.id = a.id;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION infra.enrich_facilities(OUT addressed integer, OUT permitted integer)
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
                 WHERE a2.point && ST_Expand(f2.point, 0.001)
                   AND ST_DWithin(a2.point::geography, f2.point::geography, 50)
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

    -- Уточнения редактора — поверх источников и обогащения.
    PERFORM infra.apply_curation();

    SELECT count(*) FILTER (WHERE address_unom IS NOT NULL),
           count(*) FILTER (WHERE EXISTS (SELECT 1 FROM infra.facility_source s WHERE s.facility_id = f.id AND s.relation = 'permit'))
    INTO addressed, permitted
    FROM infra.facility f WHERE f.status = 'active';
END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION infra.enrich_facilities(OUT addressed integer, OUT permitted integer)
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
                 WHERE a2.point && ST_Expand(f2.point, 0.001)
                   AND ST_DWithin(a2.point::geography, f2.point::geography, 50)
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

DROP FUNCTION infra.apply_curation();
ALTER TABLE infra.facility DROP COLUMN curated;
DROP TABLE infra.facility_report;
DROP TABLE infra.facility_curation;
