-- +goose Up

-- Ячейки H3 с объектами логистики: разрешение 8 до 11-го масштаба, дальше 9. Класс — по площади
-- застройки объектов ячейки; свойства — показатели ячейки (analytics.metric_value).
-- +goose StatementBegin
CREATE FUNCTION tiles.h3_cells(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(ST_AsMVT(t, 'h3_cells', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT c.h3::bigint AS fid, c.h3::text AS h3, c.res,
               CASE WHEN b.value >= 50000 THEN 'large' WHEN b.value >= 10000 THEN 'medium' ELSE 'small' END AS class,
               f.value AS facilities, round(b.value::numeric)::float8 AS building_area,
               round(p.value::numeric)::float8 AS population, i.value AS infra,
               round(r.value::numeric, 1)::float8 AS restricted_km, w.value AS works,
               ST_AsMVTGeom(c.geom_3857, ST_TileEnvelope(z, x, y), 4096, 8, true) AS geom
        FROM territory.h3_cell c
        JOIN analytics.metric_value f ON f.metric_code = 'h3.facilities' AND f.level = 'h3_' || c.res AND f.entity = c.h3::text
             AND f.value > 0
        JOIN analytics.metric_value b ON b.metric_code = 'h3.building_area' AND b.level = f.level AND b.entity = f.entity
        LEFT JOIN analytics.metric_value p ON p.metric_code = 'h3.population' AND p.level = f.level AND p.entity = f.entity
        LEFT JOIN analytics.metric_value i ON i.metric_code = 'h3.infra' AND i.level = f.level AND i.entity = f.entity
        LEFT JOIN analytics.metric_value r ON r.metric_code = 'h3.restricted_km' AND r.level = f.level AND r.entity = f.entity
        LEFT JOIN analytics.metric_value w ON w.metric_code = 'h3.works' AND w.level = f.level AND w.entity = f.entity
        WHERE c.res = CASE WHEN z < 11 THEN 8 ELSE 9 END AND c.geom_3857 && ST_TileEnvelope(z, x, y)
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION tiles.h3_cells(integer, integer, integer);
