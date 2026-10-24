-- Сохранённые виды карты и реестра: параметры страницы (слои, вид, фильтры, сортировка) под
-- именем; личные или общие для роли.

-- +goose Up

CREATE TABLE core.saved_view (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL REFERENCES core.app_user (id) ON DELETE CASCADE,
    page       text NOT NULL,
    name       text NOT NULL,
    params     jsonb NOT NULL DEFAULT '{}'::jsonb,
    role_code  text REFERENCES core.role (code) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saved_view_page_chk CHECK (page IN ('map', 'facilities')),
    CONSTRAINT saved_view_name_chk CHECK (length(trim(name)) BETWEEN 1 AND 120)
);

COMMENT ON COLUMN core.saved_view.params IS 'параметры адреса страницы';
COMMENT ON COLUMN core.saved_view.role_code IS 'общий вид для роли; пусто — личный';

CREATE INDEX saved_view_owner_idx ON core.saved_view (owner_id, page);
CREATE INDEX saved_view_role_idx ON core.saved_view (role_code, page) WHERE role_code IS NOT NULL;

-- +goose Down

DROP TABLE core.saved_view;
