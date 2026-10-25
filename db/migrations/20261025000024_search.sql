-- +goose Up

-- Полнотекстовый поиск по-русски без диакритики (ё → е): словарь unaccent перед стеммером.
CREATE TEXT SEARCH CONFIGURATION search.ru (COPY = pg_catalog.russian);
ALTER TEXT SEARCH CONFIGURATION search.ru
    ALTER MAPPING FOR hword, hword_part, word WITH public.unaccent, russian_stem;

-- Документ поиска — одна строка на сущность. Видимость: без владельца и роли — всем; иначе
-- владельцу и обладателям роли (сохранённые виды).
CREATE TABLE search.document (
    kind        text NOT NULL,
    ref         text NOT NULL,
    title       text NOT NULL,
    subtitle    text,
    body        text,
    point       geometry(Point, 4326),
    -- Важность сущности 0–1: усиление в ранжировании.
    weight      real NOT NULL DEFAULT 0.5 CHECK (weight BETWEEN 0 AND 1),
    owner_id    uuid,
    role_code   text,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    title_norm    text GENERATED ALWAYS AS (core.normalize_text(title)) STORED,
    subtitle_norm text GENERATED ALWAYS AS (core.normalize_text(coalesce(subtitle, ''))) STORED,
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('search.ru', title), 'A') ||
        setweight(to_tsvector('search.ru', coalesce(subtitle, '')), 'B') ||
        setweight(to_tsvector('search.ru', coalesce(body, '')), 'C')) STORED,
    PRIMARY KEY (kind, ref),
    CONSTRAINT document_kind_chk CHECK (kind IN (
        'facility', 'address', 'street', 'territory', 'dataset', 'source', 'legal_act', 'help',
        'road_work', 'saved_view', 'report'))
);

CREATE INDEX document_tsv_idx ON search.document USING gin (tsv);
CREATE INDEX document_title_trgm_idx ON search.document USING gin (title_norm gin_trgm_ops);
CREATE INDEX document_subtitle_trgm_idx ON search.document USING gin (subtitle_norm gin_trgm_ops);
CREATE INDEX document_point_idx ON search.document USING gist (point);

-- Состояние индекса по видам: подпись данных, по которым он построен.
CREATE TABLE search.state (
    kind        text PRIMARY KEY,
    signature   text NOT NULL,
    documents   integer NOT NULL,
    duration_ms integer NOT NULL,
    indexed_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE search.state;
DROP TABLE search.document;
DROP TEXT SEARCH CONFIGURATION search.ru;
