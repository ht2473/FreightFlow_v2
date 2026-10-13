-- Правила зон ограничения движения грузового транспорта с правовыми основаниями, официальные
-- страницы Дептранса для сверки и «грузовой каркас» (перечень улиц transport.mos.ru).

-- +goose Up

-- Правовые акты и официальные страницы, на которых основаны правила.
CREATE TABLE ref.legal_document (
    code         text PRIMARY KEY,
    kind         text NOT NULL,
    title        text NOT NULL,
    number       text,
    signed_on    date,
    effective_on date,
    authority    text NOT NULL,
    url          text,
    status       text NOT NULL,
    amends       text REFERENCES ref.legal_document (code),
    summary      text,
    verified_on  date,
    CONSTRAINT legal_document_kind_chk CHECK (kind IN ('act', 'amendment', 'page')),
    CONSTRAINT legal_document_status_chk CHECK (status IN ('in_force', 'repealed', 'info'))
);

COMMENT ON TABLE ref.legal_document IS
    'act — правовой акт; amendment — акт о внесении изменений (amends — изменяемый акт); page — официальная страница органа власти';
COMMENT ON COLUMN ref.legal_document.verified_on IS 'дата сверки правил с текстом документа';

-- Правило зоны: кого и когда касается ограничение в пределах кольца (вместе с самим кольцом;
-- контур — territory.ring_zone).
-- Политика проезда: для ТС, времени и пропусков правило действует, если масса больше порога
-- (или экокласс ниже минимального) и время попадает в интервал; prohibited — въезд запрещён,
-- pass_required — нужен пропуск вида pass_kind одной из зон pass_zones, frame_only — вне
-- грузового каркаса только для обслуживания адресов района.
CREATE TABLE regulation.zone_rule (
    code          text PRIMARY KEY,
    zone          text NOT NULL,
    title         text NOT NULL,
    effect        text NOT NULL,
    mass_basis    text,
    mass_over_t   numeric,
    eco_class_min smallint,
    time_from     time,
    time_to       time,
    pass_kind     text,
    pass_zones    text[],
    valid_from    date,
    valid_to      date,
    document_code text NOT NULL REFERENCES ref.legal_document (code),
    clause        text,
    confirmed     boolean NOT NULL DEFAULT false,
    confirmed_by  text REFERENCES ref.legal_document (code),
    note          text,
    ord           smallint NOT NULL DEFAULT 0,
    CONSTRAINT zone_rule_zone_chk CHECK (zone IN ('sk', 'ttk', 'mkad')),
    CONSTRAINT zone_rule_effect_chk CHECK (effect IN ('prohibited', 'pass_required', 'frame_only')),
    CONSTRAINT zone_rule_mass_chk CHECK ((mass_basis IS NULL) = (mass_over_t IS NULL)
                                         AND (mass_basis IS NULL OR mass_basis IN ('gross', 'payload'))),
    CONSTRAINT zone_rule_pass_chk CHECK ((effect = 'pass_required') = (pass_kind IS NOT NULL AND pass_zones IS NOT NULL)
                                         AND (pass_kind IS NULL OR pass_kind IN ('day', 'night'))),
    CONSTRAINT zone_rule_time_chk CHECK ((time_from IS NULL) = (time_to IS NULL)),
    CONSTRAINT zone_rule_subject_chk CHECK (mass_over_t IS NOT NULL OR eco_class_min IS NOT NULL),
    CONSTRAINT zone_rule_confirmed_chk CHECK (NOT confirmed OR confirmed_by IS NOT NULL)
);

COMMENT ON COLUMN regulation.zone_rule.mass_basis IS
    'gross — разрешённая максимальная масса; payload — грузоподъёмность (РММ минус масса без нагрузки)';
COMMENT ON COLUMN regulation.zone_rule.time_from IS 'начало действия; интервал через полночь — time_from > time_to; NULL — круглосуточно';
COMMENT ON COLUMN regulation.zone_rule.confirmed IS
    'правило сверено с официальным источником confirmed_by; неподтверждённые используются с предупреждением';

-- Официальные страницы, по которым сверены правила: при изменении текста после сверки
-- загрузка предупреждает, что правила нужно перепроверить.
CREATE TABLE regulation.official_page (
    code          text PRIMARY KEY REFERENCES ref.legal_document (code),
    title         text NOT NULL,
    content       text NOT NULL,
    content_hash  text NOT NULL,
    verified_hash text,
    fetched_at    timestamptz NOT NULL,
    changed_at    timestamptz NOT NULL,
    source_code   text NOT NULL,
    snapshot_id   uuid
);

COMMENT ON COLUMN regulation.official_page.verified_hash IS
    'хеш текста на момент сверки правил; отличие от content_hash — страница изменилась после сверки';

-- Грузовой каркас: улицы по перечню Дептранса и найденные для них участки сети.
CREATE TABLE regulation.cargo_frame_street (
    id          integer PRIMARY KEY,
    okrug       text NOT NULL,
    entry       text NOT NULL,
    street      text NOT NULL,
    extent      text,
    ring        text,
    segments    integer NOT NULL DEFAULT 0,
    length_km   double precision NOT NULL DEFAULT 0,
    source_code text NOT NULL,
    snapshot_id uuid,
    CONSTRAINT cargo_frame_street_ring_chk CHECK (ring IN ('sk', 'ttk', 'mkad'))
);

COMMENT ON COLUMN regulation.cargo_frame_street.entry IS 'строка перечня как опубликована';
COMMENT ON COLUMN regulation.cargo_frame_street.extent IS
    'границы участка («от … до …»); участки сети отмечаются по всей улице в округе';
COMMENT ON COLUMN regulation.cargo_frame_street.ring IS 'строка перечня — кольцо (СК, ТТК, МКАД): участки по линии кольца';

CREATE TABLE regulation.cargo_frame_segment (
    segment_id uuid NOT NULL REFERENCES network.road_segment (id) ON DELETE CASCADE,
    street_id  integer NOT NULL REFERENCES regulation.cargo_frame_street (id) ON DELETE CASCADE,
    partial    boolean NOT NULL,
    PRIMARY KEY (segment_id, street_id)
);

CREATE INDEX cargo_frame_segment_street_idx ON regulation.cargo_frame_segment (street_id);

-- Тайлы грузового каркаса: участки сети улиц перечня; partial — улица в перечне не целиком.
-- До 13-го масштаба участки объединяются по улицам и упрощаются до пикселя тайла.
-- +goose StatementBegin
CREATE FUNCTION tiles.cargo_frame(z integer, x integer, y integer) RETURNS bytea
    LANGUAGE sql STABLE PARALLEL SAFE AS $$
    WITH env AS (
        SELECT ST_TileEnvelope(z, x, y) AS e, (ST_XMax(ST_TileEnvelope(z, x, y)) - ST_XMin(ST_TileEnvelope(z, x, y))) / 4096 AS px
    ), f AS (
        SELECT r.id AS segment_id, r.osm_id, r.name, r.geom_3857, c.id AS street_id, c.entry, c.okrug, f.partial
        FROM regulation.cargo_frame_segment f
        JOIN regulation.cargo_frame_street c ON c.id = f.street_id
        JOIN network.road_segment r ON r.id = f.segment_id, env
        WHERE r.geom_3857 && env.e
    )
    SELECT coalesce(ST_AsMVT(t, 'cargo_frame', 4096, 'geom', 'fid'), '')
    FROM (
        SELECT street_id::bigint AS fid, NULL::text AS name, entry, okrug, partial,
               ST_AsMVTGeom(ST_Simplify(ST_LineMerge(ST_Collect(geom_3857)), env.px * 2), env.e, 4096, 16, true) AS geom
        FROM f, env
        WHERE z < 13
        GROUP BY street_id, entry, okrug, partial, env.e, env.px
        UNION ALL
        SELECT osm_id, name, min(entry), min(okrug), bool_and(partial),
               ST_AsMVTGeom(geom_3857, env.e, 4096, 16, true)
        FROM f, env
        WHERE z >= 13
        GROUP BY segment_id, osm_id, name, geom_3857, env.e
    ) t
    WHERE geom IS NOT NULL
$$;
-- +goose StatementEnd

-- Документы, по которым сверены правила 13.10.2026.
INSERT INTO ref.legal_document (code, kind, title, number, signed_on, effective_on, authority, url, status, amends, summary, verified_on) VALUES
('pp-379-2011', 'act',
 'Об ограничении движения грузового автотранспорта в городе Москве и признании утратившими силу отдельных правовых актов Правительства Москвы',
 '379-ПП', '2011-08-22', NULL, 'Правительство Москвы', 'https://base.garant.ru/397523/', 'in_force', NULL,
 'Зоны ограничения (МКАД, ТТК, Садовое кольцо), экологические классы, пропуска: дневные и ночные, разовые и до года; зоны действия пропусков «МКАД», «ТТК», «СК».',
 '2026-10-13'),
('pp-90-2014', 'amendment', 'О внесении изменений в постановление Правительства Москвы от 22 августа 2011 г. № 379-ПП',
 '90-ПП', '2014-03-04', '2017-01-01', 'Правительство Москвы', NULL, 'in_force', 'pp-379-2011',
 'Новая редакция пункта 1: экологический класс не ниже 3 в пределах ТТК и не ниже 2 в пределах МКАД с 1 января 2017 г.', '2026-10-13'),
('pp-228-2015', 'amendment', 'О внесении изменений в постановление Правительства Москвы от 22 августа 2011 г. № 379-ПП',
 '228-ПП', '2015-04-23', NULL, 'Правительство Москвы', NULL, 'in_force', 'pp-379-2011',
 'Пункт 2.1: ограничение для грузового автотранспорта грузоподъёмностью более 1 т в пределах ТТК.', '2026-10-13'),
('pp-399-2021', 'amendment', 'О внесении изменений в постановление Правительства Москвы от 22 августа 2011 г. № 379-ПП',
 '399-ПП', '2021-04-06', '2021-05-05', 'Правительство Москвы', NULL, 'in_force', 'pp-379-2011',
 'Ограничение с 7.00 до 23.00 для грузовиков разрешённой максимальной массой более 3,5 т в пределах МКАД и на МКАД.', '2026-10-13'),
('pp-1583-2026', 'amendment', 'О внесении изменения в постановление Правительства Москвы от 22 августа 2011 г. № 379-ПП',
 '1583-ПП', '2026-06-09', NULL, 'Правительство Москвы', 'https://www.garant.ru/hotlaw/moscow/2141340/', 'in_force', 'pp-379-2011',
 'Приостановка действия пропусков на участках, где Департамент транспорта вводит временные ограничения при неблагоприятных природно-климатических условиях.',
 '2026-10-13'),
('tm-cargo-pass', 'page', 'С 1 января для проезда по Москве грузовикам свыше 3,5 тонн днем и ночью необходимо иметь пропуск',
 NULL, NULL, NULL, 'Департамент транспорта и развития дорожно-транспортной инфраструктуры города Москвы',
 'https://transport.mos.ru/gruzoviki/perevod_ckad', 'info', NULL,
 'Пропуск для грузовиков более 3,5 т днём (7–23) и ночью (23–7) в границах МКАД включительно; экологические классы по зонам; ответы на вопросы.',
 '2026-10-13'),
('tm-cargo-faq', 'page', 'Часто задаваемые вопросы о грузовом транспорте', NULL, NULL, NULL,
 'Департамент транспорта и развития дорожно-транспортной инфраструктуры города Москвы',
 'https://transport.mos.ru/gruzoviki/faq', 'info', NULL, NULL, '2026-10-13'),
('tm-cargo-frame', 'page', 'Грузовой каркас', NULL, NULL, NULL,
 'Департамент транспорта и развития дорожно-транспортной инфраструктуры города Москвы',
 'https://transport.mos.ru/gruzoviki/cargoframe', 'info', NULL,
 'В жилых зонах внутри МКАД движение грузовиков разрешённой максимальной массой более 2,5 т — только для обслуживания предприятий и жителей района, с подтверждающими документами.',
 '2026-10-13'),
('tm-cargo-frame-streets', 'page', 'Улицы, входящие в грузовой каркас', NULL, NULL, NULL,
 'Департамент транспорта и развития дорожно-транспортной инфраструктуры города Москвы',
 'https://transport.mos.ru/gruzoviki/ulitsy', 'info', NULL, 'Перечень улиц грузового каркаса по административным округам.', '2026-10-13');

INSERT INTO regulation.zone_rule (code, zone, title, effect, mass_basis, mass_over_t, eco_class_min, time_from, time_to,
                                  pass_kind, pass_zones, valid_from, document_code, clause, confirmed, confirmed_by, note, ord) VALUES
('ttk_eco', 'ttk', 'Экологический класс не ниже 3 в пределах ТТК', 'prohibited', NULL, NULL, 3, NULL, NULL,
 NULL, NULL, '2017-01-01', 'pp-379-2011', 'п. 1.1 (ред. 90-ПП)', true, 'tm-cargo-pass',
 'Пропуск не выдаётся грузовикам ниже класса.', 10),
('mkad_eco', 'mkad', 'Экологический класс не ниже 2 в пределах МКАД', 'prohibited', NULL, NULL, 2, NULL, NULL,
 NULL, NULL, '2017-01-01', 'pp-379-2011', 'п. 1.2 (ред. 90-ПП)', true, 'tm-cargo-pass',
 'Внутри ТТК требование строже — класс не ниже 3.', 20),
('mkad_gross_day', 'mkad', 'Днём — пропуск для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '07:00', '23:00',
 'day', '{mkad,ttk,sk}', '2021-05-05', 'pp-379-2011', 'п. 2, 2.4 (ред. 399-ПП)', true, 'tm-cargo-pass',
 'Проезд по МКАД без въезда в пределы для грузовиков более 12 т — по пропускам Московской области (п. 13).', 30),
('mkad_gross_night', 'mkad', 'Ночью — пропуск для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '23:00', '07:00',
 'night', '{mkad,ttk,sk}', '2022-01-01', 'pp-379-2011', 'п. 4.2, 7.2.2, 7.3', true, 'tm-cargo-pass',
 'Ночное ограничение вводит Департамент транспорта по п. 4.2; реквизиты его решения не найдены.', 40),
('ttk_gross_day', 'ttk', 'Днём — пропуск зоны «ТТК» или «СК» для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '07:00', '23:00',
 'day', '{ttk,sk}', '2021-05-05', 'pp-379-2011', 'п. 2.4, 15.2, 15.3', true, 'tm-cargo-faq',
 'Пропуск зоны «МКАД» не даёт права движения в пределах ТТК и по ТТК.', 50),
('ttk_gross_night', 'ttk', 'Ночью — пропуск зоны «ТТК» или «СК» для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '23:00', '07:00',
 'night', '{ttk,sk}', '2022-01-01', 'pp-379-2011', 'п. 4.2, 7.3, 15.2, 15.3', true, 'tm-cargo-faq', NULL, 60),
('ttk_payload_day', 'ttk', 'Днём — пропуск зоны «ТТК» или «СК» при грузоподъёмности более 1 т', 'pass_required', 'payload', 1, NULL, '07:00', '23:00',
 'day', '{ttk,sk}', '2021-05-05', 'pp-379-2011', 'п. 2, 2.1 (ред. 228-ПП, 399-ПП)', false, NULL,
 'Текст пункта сверен по редакции от 15.03.2022 во вторичной публикации; на страницах Дептранса порог 1 т не упоминается.', 70),
('sk_gross_day', 'sk', 'Днём — пропуск зоны «СК» для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '07:00', '23:00',
 'day', '{sk}', '2021-05-05', 'pp-379-2011', 'п. 15.1, 15.2', false, NULL,
 'Пропуск зоны «ТТК» не даёт права движения в пределах Садового кольца (п. 15.2, вторичная публикация).', 80),
('sk_gross_night', 'sk', 'Ночью — пропуск зоны «СК» для грузовиков более 3,5 т', 'pass_required', 'gross', 3.5, NULL, '23:00', '07:00',
 'night', '{sk}', '2022-01-01', 'pp-379-2011', 'п. 7.3, 15.1, 15.2', false, NULL, NULL, 90),
('sk_payload_day', 'sk', 'Днём — пропуск зоны «СК» при грузоподъёмности более 1 т', 'pass_required', 'payload', 1, NULL, '07:00', '23:00',
 'day', '{sk}', '2021-05-05', 'pp-379-2011', 'п. 2.1, 15.1, 15.2', false, NULL, NULL, 100),
('mkad_frame', 'mkad', 'Вне грузового каркаса — только обслуживание района для грузовиков более 2,5 т', 'frame_only', 'gross', 2.5, NULL, NULL, NULL,
 NULL, NULL, NULL, 'tm-cargo-frame', NULL, true, 'tm-cargo-frame',
 'Действует в жилых зонах по дорожным знакам; нужны документы, подтверждающие адрес обслуживания.', 110);

-- +goose Down

DROP FUNCTION tiles.cargo_frame(integer, integer, integer);
DROP TABLE regulation.cargo_frame_segment;
DROP TABLE regulation.cargo_frame_street;
DROP TABLE regulation.official_page;
DROP TABLE regulation.zone_rule;
DROP TABLE ref.legal_document;
