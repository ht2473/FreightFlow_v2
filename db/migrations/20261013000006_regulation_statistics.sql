-- Регулирование (пропуска, спецразрешения), официальная статистика, адресный реестр,
-- ограничения для размещения объектов, разрешения на ввод.

-- +goose Up

-- Пропуска грузового транспорта: только агрегаты по месяцам (номера пропусков не хранятся).
CREATE TABLE regulation.pass_stat (
    month       date NOT NULL,
    zone        text NOT NULL,
    series      text NOT NULL,
    metric      text NOT NULL,
    value       integer NOT NULL,
    source_code text NOT NULL,
    snapshot_id uuid,
    PRIMARY KEY (month, zone, series, metric),
    CONSTRAINT pass_stat_metric_chk CHECK (metric IN ('valid', 'issued', 'cancelled'))
);

COMMENT ON TABLE regulation.pass_stat IS
    'valid — действующих на первое число месяца; issued — начавших действовать в месяце; cancelled — аннулированных в месяце';

-- Спецразрешения на движение тяжеловесных и крупногабаритных ТС (без сведений о перевозчике и ТС).
CREATE TABLE regulation.special_permit (
    id               uuid PRIMARY KEY,
    mos_global_id    bigint NOT NULL UNIQUE,
    issued_on        date,
    valid_from       date,
    valid_to         date,
    trips            integer,
    transport_type   text,
    carrier_type     text,
    length_m         numeric,
    width_m          numeric,
    height_m         numeric,
    mass_empty_t     numeric,
    mass_loaded_t    numeric,
    source_code      text NOT NULL,
    snapshot_id      uuid,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX special_permit_issued_idx ON regulation.special_permit (issued_on);

-- Официальная статистика ---------------------------------------------------------------

CREATE TABLE statistics.indicator (
    code         text PRIMARY KEY,
    name         text NOT NULL,
    unit         text NOT NULL,
    periodicity  text NOT NULL,
    source_code  text NOT NULL,
    description  text,
    CONSTRAINT indicator_periodicity_chk CHECK (periodicity IN ('year', 'quarter', 'month'))
);

CREATE TABLE statistics.observation (
    indicator_code text NOT NULL REFERENCES statistics.indicator (code) ON DELETE CASCADE,
    area_id        uuid REFERENCES territory.admin_area (id) ON DELETE CASCADE,
    area_name      text NOT NULL,
    period         date NOT NULL,
    value          numeric NOT NULL,
    source_code    text NOT NULL,
    snapshot_id    uuid,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN statistics.observation.period IS 'начало периода (год — 1 января)';

CREATE UNIQUE INDEX observation_key_uq ON statistics.observation (indicator_code, area_name, period);

-- Население территорий по годам (Росстат, портал Москвы).
CREATE TABLE territory.population (
    area_id      uuid REFERENCES territory.admin_area (id) ON DELETE CASCADE,
    area_name    text NOT NULL,
    year         smallint NOT NULL,
    value        integer NOT NULL,
    kind         text NOT NULL,
    source_code  text NOT NULL,
    snapshot_id  uuid,
    PRIMARY KEY (area_name, year, kind),
    CONSTRAINT population_kind_chk CHECK (kind IN ('average', 'january1'))
);

COMMENT ON COLUMN territory.population.kind IS 'average — среднегодовая численность, january1 — на 1 января';

-- Ограничения для размещения: охранные зоны и территории ОКН, парки ------------------------

CREATE TABLE territory.constraint_zone (
    id             uuid PRIMARY KEY,
    kind           text NOT NULL,
    mos_dataset    integer NOT NULL,
    mos_global_id  bigint NOT NULL,
    name           text,
    attrs          jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom           geometry(MultiPolygon, 4326) NOT NULL,
    geom_3857      geometry(MultiPolygon, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    area_m2        double precision NOT NULL,
    source_code    text NOT NULL,
    snapshot_id    uuid,
    first_seen_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT constraint_zone_kind_chk CHECK (kind IN ('heritage_protection', 'heritage_territory', 'park')),
    CONSTRAINT constraint_zone_mos_uq UNIQUE (mos_dataset, mos_global_id)
);

CREATE INDEX constraint_zone_geom_idx ON territory.constraint_zone USING gist (geom);
CREATE INDEX constraint_zone_geom_3857_idx ON territory.constraint_zone USING gist (geom_3857);

-- Ключ улицы: тип улицы в краткой форме и слова названия по алфавиту, чтобы «Проезд Огородный»
-- и «Огородный проезд» совпадали.
-- +goose StatementBegin
CREATE FUNCTION territory.street_key(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    WITH w AS (
        SELECT tok FROM regexp_split_to_table(
            regexp_replace(replace(lower(coalesce(t, '')), 'ё', 'е'), '[^а-яa-z0-9-]+', ' ', 'g'), ' ') tok
        WHERE tok <> ''
    ), m AS (
        SELECT CASE
                   WHEN tok IN ('улица', 'ул') THEN '#ул'
                   WHEN tok IN ('проезд', 'пр-д', 'пр') THEN '#пр'
                   WHEN tok IN ('проспект', 'пр-кт', 'просп', 'пр-т') THEN '#прт'
                   WHEN tok IN ('переулок', 'пер') THEN '#пер'
                   WHEN tok IN ('бульвар', 'б-р', 'бул') THEN '#бул'
                   WHEN tok IN ('шоссе', 'ш') THEN '#ш'
                   WHEN tok IN ('набережная', 'наб') THEN '#наб'
                   WHEN tok IN ('площадь', 'пл') THEN '#пл'
                   WHEN tok IN ('тупик', 'туп') THEN '#туп'
                   WHEN tok IN ('аллея', 'ал') THEN '#ал'
                   WHEN tok IN ('квартал', 'кв-л') THEN '#кв'
                   WHEN tok IN ('микрорайон', 'мкр') THEN '#мкр'
                   WHEN tok IN ('город', 'г', 'москва', 'российская', 'федерация', 'внутригородская', 'территория',
                                'муниципальный', 'округ', 'поселение', 'поселок', 'дачный', 'деревня', 'село') THEN NULL
                   ELSE tok
               END AS tok
        FROM w
    )
    SELECT nullif(coalesce(min(tok) FILTER (WHERE tok LIKE '#%'), '') || ':' ||
                  coalesce(string_agg(tok, ' ' ORDER BY tok) FILTER (WHERE tok NOT LIKE '#%'), ''), ':')
    FROM m
$$;
-- +goose StatementEnd

-- Номер (дом, корпус, строение) в общем виде: нижний регистр, без пробелов.
CREATE FUNCTION territory.house_key(v text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    RETURN nullif(regexp_replace(lower(coalesce(v, '')), '\s+', '', 'g'), '');

-- Ключ адреса из свободного текста. Адрес делится на части запятыми; номер дома — после слов
-- «дом», «владение» и сокращений; улица — перед номером в той же части или в ближайшей
-- предыдущей части с типом улицы; корпус и строение — в части с домом или в следующих.
-- Первая строка адреса (в разрешениях бывает несколько адресов через перевод строки).
-- +goose StatementBegin
CREATE FUNCTION territory.address_key(addr text) RETURNS text
    LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
    parts text[];
    s text;
    rest text;
    house text;
    street text;
    prev_street text;
BEGIN
    parts := regexp_split_to_array(split_part(coalesce(addr, ''), chr(10), 1), ',');
    FOR i IN 1 .. coalesce(array_length(parts, 1), 0) LOOP
        s := ' ' || regexp_replace(replace(lower(parts[i]), 'ё', 'е'), '[.«»"()]', ' ', 'g') || ' ';
        house := substring(s FROM '\s(?:дом|д|владение|вл)\s+([0-9][^\s]*)');
        IF house IS NULL THEN
            street := territory.street_key(s);
            IF street LIKE '#%' THEN
                prev_street := street;
            END IF;
            CONTINUE;
        END IF;
        street := territory.street_key(substring(s FROM '^(.*?)\s(?:дом|д|владение|вл)\s'));
        IF street IS NULL OR street NOT LIKE '#%' THEN
            street := prev_street;
        END IF;
        rest := s || ' ' || regexp_replace(lower(coalesce(parts[i + 1], '') || ' ' || coalesce(parts[i + 2], '')),
                                            '[.«»"()]', ' ', 'g') || ' ';
        RETURN coalesce(street, '') || '|' || territory.house_key(house) || '|' ||
               coalesce(territory.house_key(substring(rest FROM '\s(?:корпус|корп|к)\s+([0-9а-я][^\s]*)')), '') || '|' ||
               coalesce(territory.house_key(substring(rest FROM '\s(?:строение|стр|с)\s+([0-9][^\s]*)')), '');
    END LOOP;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- Адресный реестр объектов недвижимости ------------------------------------------------------

CREATE TABLE territory.address (
    unom            bigint PRIMARY KEY,
    mos_global_id   bigint NOT NULL UNIQUE,
    address         text NOT NULL,
    simple_address  text,
    obj_type        text,
    municipal       text,
    adm_area        text,
    settlement      text,
    street          text,
    house           text,
    korpus          text,
    building        text,
    status          text,
    fias            text,
    cadastral       text[] NOT NULL DEFAULT '{}',
    geom            geometry(Geometry, 4326),
    point           geometry(Point, 4326),
    source_code     text NOT NULL,
    snapshot_id     uuid,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE territory.address IS 'адресный реестр Москвы (набор 60562): основа адресов объектов и геокодера';

CREATE INDEX address_geom_idx ON territory.address USING gist (geom);
CREATE INDEX address_point_idx ON territory.address USING gist (point);
CREATE INDEX address_simple_trgm_idx ON territory.address USING gin (simple_address gin_trgm_ops);

ALTER TABLE territory.address ADD COLUMN address_key text GENERATED ALWAYS AS (
    territory.street_key(street) || '|' || coalesce(territory.house_key(house), '') || '|' ||
    coalesce(territory.house_key(korpus), '') || '|' || coalesce(territory.house_key(building), '')) STORED;

CREATE INDEX address_key_idx ON territory.address (address_key);

-- Разрешения на ввод объектов в эксплуатацию (без сведений о застройщике).
CREATE TABLE infra.commissioning (
    id               uuid PRIMARY KEY,
    mos_global_id    bigint NOT NULL UNIQUE,
    permit_number    text,
    permit_date      date,
    object_name      text,
    address          text,
    adm_area         text,
    district         text,
    work_type        text,
    purpose          text,
    funding          text,
    area_m2          numeric,
    length_m         numeric,
    logistics        boolean NOT NULL DEFAULT false,
    address_unom     bigint REFERENCES territory.address (unom) ON DELETE SET NULL,
    point            geometry(Point, 4326),
    facility_id      uuid REFERENCES infra.facility (id) ON DELETE SET NULL,
    source_code      text NOT NULL,
    snapshot_id      uuid,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN infra.commissioning.logistics IS 'объект складского или логистического назначения (по назначению и названию)';
COMMENT ON COLUMN infra.commissioning.point IS 'по адресному реестру: точка здания с совпадающим адресом';

CREATE INDEX commissioning_date_idx ON infra.commissioning (permit_date);
CREATE INDEX commissioning_point_idx ON infra.commissioning USING gist (point);

-- +goose Down

DROP TABLE infra.commissioning;
DROP FUNCTION territory.address_key(text);
DROP TABLE territory.address;
DROP FUNCTION territory.house_key(text);
DROP FUNCTION territory.street_key(text);
DROP TABLE territory.constraint_zone;
DROP TABLE territory.population;
DROP TABLE statistics.observation;
DROP TABLE statistics.indicator;
DROP TABLE regulation.special_permit;
DROP TABLE regulation.pass_stat;
