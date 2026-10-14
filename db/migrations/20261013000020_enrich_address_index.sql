-- Адрес объекта без контура — ближайший в 50 м: отбор по индексу точек адресов (рамка 0,001°,
-- не меньше 60 м), затем точное расстояние. Без рамки сравнивались все адреса реестра.

-- +goose Up

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
