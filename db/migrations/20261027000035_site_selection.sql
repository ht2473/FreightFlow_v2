-- +goose Up

-- Сохранённые оценки локации: параметры (критерии, веса, исключения) и лучшие кандидаты на момент
-- расчёта; карта пригодности считается заново при открытии.
CREATE TABLE scenario.site_selection (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    name       text NOT NULL,
    params     jsonb NOT NULL,
    summary    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_selection_name_chk CHECK (length(name) BETWEEN 1 AND 200)
);

CREATE INDEX site_selection_user_idx ON scenario.site_selection (user_id, created_at DESC);

-- +goose Down
DROP TABLE scenario.site_selection;
