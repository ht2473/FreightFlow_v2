-- Производственный календарь РФ: рабочие, нерабочие и сокращённые дни — признак для прогнозов
-- нагрузки и сезонности.

-- +goose Up

CREATE TABLE ref.calendar_day (
    day         date PRIMARY KEY,
    kind        text NOT NULL,
    preliminary boolean NOT NULL DEFAULT false,
    source_code text NOT NULL,
    snapshot_id uuid,
    CONSTRAINT calendar_day_kind_chk CHECK (kind IN ('workday', 'dayoff', 'short', 'nonworking_paid'))
);

COMMENT ON TABLE ref.calendar_day IS
    'workday — рабочий; dayoff — выходной или праздничный; short — сокращённый предпраздничный; nonworking_paid — нерабочий оплачиваемый (указы 2020–2021 гг.)';
COMMENT ON COLUMN ref.calendar_day.preliminary IS
    'год ещё не наступил: переносы выходных утверждаются постановлением Правительства, обычно осенью';

-- +goose Down

DROP TABLE ref.calendar_day;
