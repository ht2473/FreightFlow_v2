-- Грузовой каркас и страницы правил transport.mos.ru (временные таблицы tm_street: ord, okrug,
-- entry; tm_page: code, url, title, content, content_hash) → regulation.official_page,
-- regulation.cargo_frame_street и regulation.cargo_frame_segment.

-- Страницы. Первая загрузка фиксирует текст, по которому сверены правила (verified_on
-- документа); дальше отличие хеша означает изменение страницы после сверки.
INSERT INTO regulation.official_page (code, title, content, content_hash, verified_hash, fetched_at, changed_at,
                                      source_code, snapshot_id)
SELECT p.code, p.title, p.content, p.content_hash,
       CASE WHEN d.verified_on IS NOT NULL THEN p.content_hash END,
       now(), now(), current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM tm_page p
JOIN ref.legal_document d ON d.code = p.code
ON CONFLICT (code) DO UPDATE SET
    title = EXCLUDED.title, content = EXCLUDED.content,
    changed_at = CASE WHEN regulation.official_page.content_hash <> EXCLUDED.content_hash THEN now()
                      ELSE regulation.official_page.changed_at END,
    content_hash = EXCLUDED.content_hash, fetched_at = now(), snapshot_id = EXCLUDED.snapshot_id;

-- Строки перечня: название улицы и уточнение участка («от … до …», «кроме участка …»,
-- «(исключая дублёры)»), кольцо. МСД в OSM называется полностью.
CREATE TEMP TABLE st ON COMMIT DROP AS
WITH p AS (
    SELECT s.ord, s.okrug, s.entry,
           btrim(regexp_replace(s.entry,
                 '(\s*,\s*|\s+)(от|до|кроме|исключая|включая|в сторону|со стороны)\s.*$|\s*\(.*$|\s+д\.\s*\d.*$', '', 'i')) AS street
    FROM tm_street s
)
SELECT ord AS id, okrug, entry,
       CASE WHEN street ~* '^мсд$' THEN 'Московский скоростной диаметр' ELSE street END AS street,
       nullif(btrim(substr(entry, length(street) + 1), ' ,.'), '') AS extent
FROM p;

ALTER TABLE st ADD COLUMN ring text, ADD COLUMN skey text, ADD COLUMN okrug_geom geometry;

UPDATE st SET ring = CASE
    WHEN lower(street) IN ('ттк', 'третье транспортное кольцо') THEN 'ttk'
    WHEN lower(street) IN ('мкад', 'московская кольцевая автомобильная дорога') THEN 'mkad'
    WHEN lower(street) = 'садовое кольцо' THEN 'sk' END;

UPDATE st SET skey = territory.street_key(street) WHERE ring IS NULL;

UPDATE st SET okrug_geom = a.geom
FROM territory.admin_area a
WHERE a.kind = 'okrug' AND lower(a.name) = lower(st.okrug);

-- Участки сети в округах перечня с ключами названий: основное, официальное, другое, название
-- моста (у мостов и эстакад в OSM — отдельным тегом или статьёй Википедии).
CREATE TEMP TABLE seg ON COMMIT DROP AS
SELECT DISTINCT r.id, r.geom, r.length_m, territory.street_key(n) AS skey
FROM network.road_segment r
JOIN (SELECT DISTINCT okrug_geom FROM st WHERE okrug_geom IS NOT NULL) o ON ST_Intersects(r.geom, o.okrug_geom)
CROSS JOIN LATERAL unnest(ARRAY[r.name, r.tags->>'official_name', r.tags->>'alt_name', r.tags->>'bridge:name',
                                CASE WHEN r.bridge THEN regexp_replace(r.tags->>'wikipedia', '^ru:', '') END]) n
WHERE n IS NOT NULL AND r.class NOT IN ('service', 'track');

CREATE INDEX ON seg (split_part(skey, ':', 2));
CREATE INDEX ON seg USING gist (geom);

-- Улица — по названию в своём округе; тип улицы сравнивается, только если указан в перечне
-- («Баррикадная» — любая Баррикадная, «Баррикадная улица» — только улица).
CREATE TEMP TABLE matched ON COMMIT DROP AS
SELECT DISTINCT s.id AS street_id, g.id AS segment_id, g.length_m
FROM st s
JOIN seg g ON split_part(g.skey, ':', 2) = split_part(s.skey, ':', 2)
          AND (split_part(s.skey, ':', 1) = '' OR split_part(g.skey, ':', 1) = split_part(s.skey, ':', 1))
          AND ST_Intersects(g.geom, s.okrug_geom)
WHERE s.ring IS NULL AND s.skey IS NOT NULL AND split_part(s.skey, ':', 2) <> '';

-- Кольцо — участки магистралей вдоль линии кольца в своём округе (не менее 70 % длины в 25 м).
CREATE TEMP TABLE ring_buf ON COMMIT DROP AS
SELECT code, ST_Buffer(line::geography, 25)::geometry AS buf
FROM territory.ring_zone
WHERE code IN (SELECT ring FROM st);

INSERT INTO matched (street_id, segment_id, length_m)
SELECT s.id, r.id, r.length_m
FROM st s
JOIN ring_buf b ON b.code = s.ring
JOIN network.road_segment r ON r.geom && b.buf AND ST_Intersects(r.geom, b.buf)
WHERE r.class IN ('motorway', 'trunk', 'primary', 'secondary')
  AND ST_Intersects(r.geom, s.okrug_geom)
  AND ST_Length(ST_Intersection(r.geom, b.buf)::geography) >= 0.7 * r.length_m;

DELETE FROM regulation.cargo_frame_street WHERE source_code = current_setting('ff.source');

INSERT INTO regulation.cargo_frame_street (id, okrug, entry, street, extent, ring, segments, length_km, source_code, snapshot_id)
SELECT s.id, s.okrug, s.entry, s.street, s.extent, s.ring,
       (SELECT count(*) FROM matched m WHERE m.street_id = s.id),
       coalesce((SELECT sum(m.length_m) FROM matched m WHERE m.street_id = s.id), 0) / 1000,
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid
FROM st s;

INSERT INTO regulation.cargo_frame_segment (segment_id, street_id, partial)
SELECT DISTINCT m.segment_id, m.street_id, s.extent IS NOT NULL
FROM matched m
JOIN st s ON s.id = m.street_id;

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'streets' AS key, count(*) AS value FROM st
UNION ALL SELECT 'streets_partial', count(*) FROM st WHERE extent IS NOT NULL
UNION ALL SELECT 'streets_matched', count(*) FROM regulation.cargo_frame_street WHERE segments > 0
UNION ALL SELECT 'streets_matched_pct',
                 round(100.0 * count(*) FILTER (WHERE segments > 0) / greatest(count(*), 1))
          FROM regulation.cargo_frame_street
UNION ALL SELECT 'okrugs_unmatched', count(DISTINCT okrug) FROM st WHERE okrug_geom IS NULL
UNION ALL SELECT 'frame_segments', count(DISTINCT segment_id) FROM regulation.cargo_frame_segment
UNION ALL SELECT 'frame_km', round(sum(r.length_m) / 1000)
          FROM network.road_segment r WHERE r.id IN (SELECT segment_id FROM regulation.cargo_frame_segment)
UNION ALL SELECT 'pages', count(*) FROM tm_page
UNION ALL SELECT 'pages_changed', count(*) FROM regulation.official_page WHERE verified_hash IS DISTINCT FROM content_hash;
