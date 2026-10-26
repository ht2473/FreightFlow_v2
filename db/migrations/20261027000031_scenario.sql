-- +goose Up

-- Сценарии «что если»: параметры, ход расчёта и итог. База и сценарий считаются одной задачей
-- с одинаковыми параметрами, различаются только изменения сценария.
CREATE TABLE scenario.scenario (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    params         jsonb NOT NULL,
    status         text NOT NULL DEFAULT 'queued',
    progress       jsonb NOT NULL DEFAULT '{}'::jsonb,
    summary        jsonb,
    error          text,
    graph_built_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    finished_at    timestamptz,
    CONSTRAINT scenario_status_chk CHECK (status IN ('queued', 'running', 'done', 'failed')),
    CONSTRAINT scenario_name_chk CHECK (length(name) BETWEEN 1 AND 200)
);

COMMENT ON COLUMN scenario.scenario.params IS
    'профиль ТС, учёт зон и пропуска, время, переопределения зон, перекрытия, новый объект, цели, изохроны';
COMMENT ON COLUMN scenario.scenario.progress IS 'ход расчёта: этап, выполнено и всего запросов';
COMMENT ON COLUMN scenario.scenario.summary IS 'итог: затронутые объекты, прирост времени, потеря доступа, перекрытые участки';
COMMENT ON COLUMN scenario.scenario.graph_built_at IS 'время сборки графа маршрутизации, на котором посчитан сценарий';

CREATE INDEX scenario_user_idx ON scenario.scenario (user_id, created_at DESC);

-- Время до ближайшей цели каждого вида: база и сценарий. entity — id объекта реестра или new
-- (гипотетический объект). Статус: ok, unreachable (путь не найден), blocked (в закрытой зоне).
CREATE TABLE scenario.facility_delta (
    scenario_id     uuid NOT NULL REFERENCES scenario.scenario (id) ON DELETE CASCADE,
    entity          text NOT NULL,
    target_kind     text NOT NULL,
    base_min        real,
    scenario_min    real,
    base_status     text NOT NULL,
    scenario_status text NOT NULL,
    PRIMARY KEY (scenario_id, target_kind, entity),
    CONSTRAINT facility_delta_status_chk CHECK (base_status IN ('ok', 'unreachable', 'blocked')
                                                AND scenario_status IN ('ok', 'unreachable', 'blocked'))
);

-- Изохроны базы и сценария: origin — id объекта реестра или new.
CREATE TABLE scenario.isochrone (
    scenario_id uuid NOT NULL REFERENCES scenario.scenario (id) ON DELETE CASCADE,
    origin      text NOT NULL,
    minutes     integer NOT NULL,
    variant     text NOT NULL,
    geom        geometry(MultiPolygon, 4326) NOT NULL,
    area_km2    double precision NOT NULL,
    PRIMARY KEY (scenario_id, origin, minutes, variant),
    CONSTRAINT isochrone_variant_chk CHECK (variant IN ('base', 'scenario'))
);

-- +goose Down
DROP TABLE scenario.isochrone;
DROP TABLE scenario.facility_delta;
DROP TABLE scenario.scenario;
