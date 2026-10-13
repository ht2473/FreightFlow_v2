-- Оперативная обстановка: дорожные работы и перекрытия с жизненным циклом, журнал изменений,
-- влияние на сеть, ежедневные агрегаты.

-- +goose Up

CREATE TABLE operations.road_work (
    id              uuid PRIMARY KEY,
    mos_dataset     integer NOT NULL,
    mos_global_id   bigint NOT NULL,
    kind            text NOT NULL,
    title           text NOT NULL,
    place           text,
    adm_area        text,
    district        text,
    source_status   text,
    planned_start   date,
    planned_end     date,
    actual_start    date,
    actual_end      date,
    period          tstzrange,
    customer        text,
    contractor      text,
    closure         boolean NOT NULL DEFAULT false,
    lanes_closed    text,
    attrs           jsonb NOT NULL DEFAULT '{}'::jsonb,
    geom            geometry(Geometry, 4326),
    geom_3857       geometry(Geometry, 3857) GENERATED ALWAYS AS (ST_Transform(geom, 3857)) STORED,
    state           text NOT NULL DEFAULT 'planned',
    significance    text,
    content_hash    text NOT NULL,
    source_code     text NOT NULL,
    snapshot_id     uuid,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    changed_at      timestamptz NOT NULL DEFAULT now(),
    removed_at      timestamptz,
    CONSTRAINT road_work_mos_uq UNIQUE (mos_dataset, mos_global_id),
    CONSTRAINT road_work_kind_chk CHECK (kind IN ('repair', 'emergency', 'closure')),
    CONSTRAINT road_work_state_chk CHECK (state IN ('planned', 'active', 'finished', 'removed')),
    CONSTRAINT road_work_significance_chk CHECK (significance IS NULL OR significance IN ('low', 'medium', 'high'))
);

COMMENT ON COLUMN operations.road_work.kind IS
    'repair — плановый ремонт (62101 и история 2016–2019), emergency — аварийно-восстановительный ремонт (62461), '
    'closure — ограничение движения на период работ (2527)';
COMMENT ON COLUMN operations.road_work.period IS 'действие: фактические даты, а без них плановые; для перекрытий — время перекрытия';
COMMENT ON COLUMN operations.road_work.state IS 'по сегодняшней дате и статусу источника; removed — запись исчезла с портала';
COMMENT ON COLUMN operations.road_work.content_hash IS 'хеш содержательных полей: изменение — событие «изменилась»';

CREATE INDEX road_work_state_idx ON operations.road_work (state);
CREATE INDEX road_work_period_idx ON operations.road_work USING gist (period);
CREATE INDEX road_work_geom_idx ON operations.road_work USING gist (geom);
CREATE INDEX road_work_geom_3857_idx ON operations.road_work USING gist (geom_3857);

-- Журнал жизненного цикла работ.
CREATE TABLE operations.road_work_event (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    work_id   uuid NOT NULL REFERENCES operations.road_work (id) ON DELETE CASCADE,
    at        timestamptz NOT NULL DEFAULT now(),
    type      text NOT NULL,
    details   jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT road_work_event_type_chk CHECK (type IN ('appeared', 'changed', 'started', 'finished', 'removed'))
);

CREATE INDEX road_work_event_work_idx ON operations.road_work_event (work_id, at);
CREATE INDEX road_work_event_at_idx ON operations.road_work_event (at);

-- Влияние работы на сеть: затронутые участки в пределах 15 м от геометрии работы.
CREATE TABLE operations.road_work_impact (
    work_id     uuid NOT NULL REFERENCES operations.road_work (id) ON DELETE CASCADE,
    segment_id  uuid NOT NULL REFERENCES network.road_segment (id) ON DELETE CASCADE,
    class       text NOT NULL,
    bridge      boolean NOT NULL,
    length_m    real NOT NULL,
    PRIMARY KEY (work_id, segment_id)
);

-- Объекты рядом с работой (до 500 м): подъезд может быть затруднён. Оценка по расстоянию;
-- с маршрутизацией (Б4) — по доступности.
CREATE TABLE operations.road_work_facility (
    work_id      uuid NOT NULL REFERENCES operations.road_work (id) ON DELETE CASCADE,
    facility_id  uuid NOT NULL REFERENCES infra.facility (id) ON DELETE CASCADE,
    distance_m   real NOT NULL,
    PRIMARY KEY (work_id, facility_id)
);

CREATE TABLE operations.daily_snapshot (
    day     date NOT NULL,
    metric  text NOT NULL,
    scope   text NOT NULL DEFAULT 'city',
    value   numeric NOT NULL,
    PRIMARY KEY (day, metric, scope)
);

COMMENT ON TABLE operations.daily_snapshot IS 'ежедневные агрегаты для временных рядов (активные работы по видам и значимости)';

-- Состояние работы на сегодня (Москва) по датам и статусу источника.
-- +goose StatementBegin
CREATE FUNCTION operations.work_state(p tstzrange, source_status text, removed boolean) RETURNS text
    LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN removed THEN 'removed'
        WHEN lower(coalesce(source_status, '')) ~ '(законч|выполнен|заверш)' THEN 'finished'
        WHEN p IS NULL THEN 'active'
        WHEN upper(p) IS NOT NULL AND upper(p) <= now() THEN 'finished'
        WHEN lower(p) IS NULL OR lower(p) <= now() THEN 'active'
        ELSE 'planned'
    END
$$;
-- +goose StatementEnd

-- Пересчёт состояний по сегодняшней дате с записью событий; возвращает число переходов.
-- Вызывается после загрузки наборов и ежедневной задачей.
-- +goose StatementBegin
CREATE FUNCTION operations.refresh_work_states() RETURNS integer
    LANGUAGE plpgsql AS $$
DECLARE
    n integer;
BEGIN
    WITH changed AS (
        UPDATE operations.road_work w
        SET state = s.new_state, changed_at = now()
        FROM (SELECT id, operations.work_state(period, source_status, removed_at IS NOT NULL) AS new_state
              FROM operations.road_work) s
        WHERE s.id = w.id AND s.new_state <> w.state
        RETURNING w.id, s.new_state
    )
    INSERT INTO operations.road_work_event (work_id, type, details)
    SELECT id, CASE new_state WHEN 'active' THEN 'started' WHEN 'finished' THEN 'finished'
                              WHEN 'removed' THEN 'removed' ELSE 'changed' END,
           jsonb_build_object('state', new_state)
    FROM changed;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END
$$;
-- +goose StatementEnd

-- Ежедневный снимок: активные работы по видам и значимости, перекрытия.
-- +goose StatementBegin
CREATE FUNCTION operations.take_daily_snapshot(d date) RETURNS void
    LANGUAGE sql AS $$
    INSERT INTO operations.daily_snapshot (day, metric, scope, value)
    SELECT d, m.metric, 'city', m.value FROM (
        SELECT 'works_active' AS metric, count(*)::numeric AS value FROM operations.road_work WHERE state = 'active'
        UNION ALL SELECT 'works_active_' || kind, count(*) FROM operations.road_work WHERE state = 'active' GROUP BY kind
        UNION ALL SELECT 'works_active_high', count(*) FROM operations.road_work WHERE state = 'active' AND significance = 'high'
        UNION ALL SELECT 'works_planned', count(*) FROM operations.road_work WHERE state = 'planned'
    ) m
    ON CONFLICT (day, metric, scope) DO UPDATE SET value = EXCLUDED.value
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION operations.take_daily_snapshot(date);
DROP FUNCTION operations.refresh_work_states();
DROP FUNCTION operations.work_state(tstzrange, text, boolean);
DROP TABLE operations.daily_snapshot;
DROP TABLE operations.road_work_facility;
DROP TABLE operations.road_work_impact;
DROP TABLE operations.road_work_event;
DROP TABLE operations.road_work;
