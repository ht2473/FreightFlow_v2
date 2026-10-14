-- Разбор величин ограничений: substring с группой в шаблоне возвращал группу (дробную часть),
-- целые значения («4») отбрасывались. Группа незахватывающая — возвращается всё число.

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION network.parse_measure(raw text) RETURNS numeric
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN raw IS NULL OR lower(trim(raw)) IN ('none', 'no', 'default', 'unsigned', 'below_default') THEN NULL
        WHEN raw ~* '^\s*[0-9]+([.,][0-9]+)?\s*(t|т|m|м)?\s*$'
            THEN replace(substring(raw FROM '[0-9]+(?:[.,][0-9]+)?'), ',', '.')::numeric
        WHEN raw ~* '^\s*[0-9]+\s*(kg|кг)\s*$'
            THEN substring(raw FROM '[0-9]+')::numeric / 1000
        ELSE NULL
    END
$$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION network.parse_measure(raw text) RETURNS numeric
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN raw IS NULL OR lower(trim(raw)) IN ('none', 'no', 'default', 'unsigned', 'below_default') THEN NULL
        WHEN raw ~* '^\s*[0-9]+([.,][0-9]+)?\s*(t|т)?\s*$'
            THEN replace(substring(raw FROM '[0-9]+([.,][0-9]+)?'), ',', '.')::numeric
        WHEN raw ~* '^\s*[0-9]+([.,][0-9]+)?\s*(m|м)\s*$'
            THEN replace(substring(raw FROM '[0-9]+([.,][0-9]+)?'), ',', '.')::numeric
        WHEN raw ~* '^\s*[0-9]+\s*(kg|кг)\s*$'
            THEN substring(raw FROM '[0-9]+')::numeric / 1000
        ELSE NULL
    END
$$;
-- +goose StatementEnd
