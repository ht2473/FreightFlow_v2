-- Профили ТС для маршрутизации: масса, нагрузка на ось, габариты, экологический класс.

-- +goose Up

CREATE TABLE ref.vehicle_profile (
    code         text PRIMARY KEY,
    name         text NOT NULL,
    gross_t      numeric NOT NULL,
    payload_t    numeric NOT NULL,
    axle_load_t  numeric NOT NULL,
    height_m     numeric NOT NULL,
    width_m      numeric NOT NULL,
    length_m     numeric NOT NULL,
    eco_class    smallint NOT NULL,
    hazmat       boolean NOT NULL DEFAULT false,
    ord          smallint NOT NULL DEFAULT 0,
    CONSTRAINT vehicle_profile_eco_chk CHECK (eco_class BETWEEN 0 AND 6)
);

COMMENT ON COLUMN ref.vehicle_profile.gross_t IS 'разрешённая максимальная масса, т';
COMMENT ON COLUMN ref.vehicle_profile.eco_class IS 'экологический класс (0 — не указан)';

-- Типовые грузовые ТС городской логистики; габариты — по типичным моделям классов.
INSERT INTO ref.vehicle_profile (code, name, gross_t, payload_t, axle_load_t, height_m, width_m, length_m, eco_class, ord) VALUES
('van_35', 'Фургон до 3,5 т', 3.5, 1.5, 2, 2.8, 2.1, 6, 5, 10),
('truck_7', 'Грузовик 7,5 т', 7.5, 4, 5, 3.4, 2.5, 8, 5, 20),
('truck_12', 'Грузовик 12 т', 12, 6, 8, 3.6, 2.55, 9, 5, 30),
('truck_20', 'Грузовик 20 т', 20, 11, 10, 4, 2.55, 10, 5, 40),
('tractor_40', 'Седельный тягач с полуприцепом 40 т', 40, 24, 10, 4, 2.55, 16.5, 5, 50);

-- +goose Down

DROP TABLE ref.vehicle_profile;
