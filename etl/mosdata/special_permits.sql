-- Спецразрешения на тяжеловесные и крупногабаритные ТС (набор 2582; проекция без сведений
-- о перевозчике и ТС) → regulation.special_permit.

INSERT INTO regulation.special_permit (id, mos_global_id, issued_on, valid_from, valid_to, trips, transport_type,
                                       carrier_type, length_m, width_m, height_m, mass_empty_t, mass_loaded_t,
                                       source_code, snapshot_id, first_seen_at, last_seen_at)
SELECT uuidv7(), r.global_id, mos_stage.date(r.cells->>'IssuedDate'),
       mos_stage.date(split_part(r.cells->>'PeriodOfValidity', '-', 1)),
       mos_stage.date(trim(split_part(r.cells->>'PeriodOfValidity', '-', 2))),
       mos_stage.num(r.cells->'NumberOfTrips')::int, mos_stage.txt(r.cells->'TypeOfTransportation'),
       mos_stage.txt(r.cells->'CarrierType'),
       mos_stage.num(r.cells->'AutotrainLength'), mos_stage.num(r.cells->'AutotrainWidth'),
       mos_stage.num(r.cells->'AutotrainHeight'), mos_stage.num(r.cells->'AutotrainWeightWithoutCargo'),
       mos_stage.num(r.cells->'AutotrainWeightWithCargo'),
       current_setting('ff.source'), current_setting('ff.snapshot')::uuid, now(), now()
FROM mos_stage.record r
WHERE r.dataset = current_setting('ff.dataset')::int
ON CONFLICT (mos_global_id) DO UPDATE SET
    issued_on = EXCLUDED.issued_on, valid_from = EXCLUDED.valid_from, valid_to = EXCLUDED.valid_to,
    trips = EXCLUDED.trips, transport_type = EXCLUDED.transport_type, carrier_type = EXCLUDED.carrier_type,
    length_m = EXCLUDED.length_m, width_m = EXCLUDED.width_m, height_m = EXCLUDED.height_m,
    mass_empty_t = EXCLUDED.mass_empty_t, mass_loaded_t = EXCLUDED.mass_loaded_t,
    snapshot_id = EXCLUDED.snapshot_id, last_seen_at = now();

DELETE FROM regulation.special_permit WHERE source_code = current_setting('ff.source') AND last_seen_at < now();

CREATE TEMP TABLE load_stats ON COMMIT DROP AS
SELECT 'permits' AS key, count(*) AS value FROM regulation.special_permit
UNION ALL SELECT 'permits_new', count(*) FROM regulation.special_permit WHERE first_seen_at = now()
UNION ALL SELECT 'permits_valid_now', count(*) FROM regulation.special_permit
          WHERE valid_from <= current_date AND valid_to >= current_date;
