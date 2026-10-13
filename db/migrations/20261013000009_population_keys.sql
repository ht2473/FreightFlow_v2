-- Население по муниципальным образованиям (Росстат): код ОКТМО и сопоставление названий
-- муниципальных образований с районами.

-- +goose Up

ALTER TABLE territory.population ADD COLUMN oktmo text;

COMMENT ON COLUMN territory.population.oktmo IS 'код ОКТМО муниципального образования (Росстат)';

-- Ключ названия территории: без служебных слов («район», «поселение», «муниципальный округ»),
-- прилагательные сведены к основе, чтобы «Можайское» (муниципальный округ) совпадало с
-- «Можайский район», а «Басманное» — с «Басманный район».
-- +goose StatementBegin
CREATE FUNCTION territory.area_name_key(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT string_agg(
               CASE WHEN length(w) > 5 THEN regexp_replace(w, '(ский|ское|ская|ской|цкий|цкое|ый|ий|ой|ое|ая|ые)$', '')
                    ELSE w END,
               ' ' ORDER BY w)
    FROM regexp_split_to_table(
             regexp_replace(replace(lower(coalesce(t, '')), 'ё', 'е'), '[^а-яa-z0-9-]+', ' ', 'g'), ' ') w
    WHERE w <> '' AND w NOT IN ('район', 'поселение', 'муниципальный', 'округ', 'внутригородская', 'территория',
                                'городской', 'город', 'административный')
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION territory.area_name_key(text);
ALTER TABLE territory.population DROP COLUMN oktmo;
