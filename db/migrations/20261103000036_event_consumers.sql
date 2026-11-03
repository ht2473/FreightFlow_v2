-- +goose Up

-- Транзакция, записавшая событие: постоянные потребители читают только события завершённых
-- транзакций (xid меньше границы снимка), поэтому запись, зафиксированная позже записи с большим
-- номером, не пропускается.
ALTER TABLE notify.outbox ADD COLUMN xid xid8 NOT NULL DEFAULT pg_current_xact_id();

CREATE INDEX outbox_xid_idx ON notify.outbox (xid, id);

-- Позиция постоянного потребителя событий: обработаны все события до (last_xid, last_id)
-- включительно. Сдвигается в одной транзакции с обработкой (platform/events.Consumer).
CREATE TABLE notify.consumer (
    name       text PRIMARY KEY,
    last_xid   xid8 NOT NULL,
    last_id    bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE notify.consumer;
DROP INDEX notify.outbox_xid_idx;
ALTER TABLE notify.outbox DROP COLUMN xid;
