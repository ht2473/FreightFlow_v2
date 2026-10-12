-- Стиль импорта OpenStreetMap (osm2pgsql, flex) для FreightFlow.
-- Берутся только классы, нужные системе: объекты логистики и промзоны, точечная
-- инфраструктура, дорожная сеть с ограничениями, административные границы, кольца.
-- Результат — промежуточная схема osm_stage; в домен данные переносит transform.sql.

local tables = {}

tables.points = osm2pgsql.define_table({
    name = 'points',
    ids = { type = 'node', id_column = 'osm_id' },
    columns = {
        { column = 'kind', type = 'text', not_null = true },
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'point', projection = 4326, not_null = true },
    },
})

tables.roads = osm2pgsql.define_table({
    name = 'roads',
    ids = { type = 'way', id_column = 'osm_id' },
    columns = {
        { column = 'class', type = 'text', not_null = true },
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'linestring', projection = 4326, not_null = true },
    },
})

tables.rails = osm2pgsql.define_table({
    name = 'rails',
    ids = { type = 'way', id_column = 'osm_id' },
    columns = {
        { column = 'kind', type = 'text', not_null = true },
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'linestring', projection = 4326, not_null = true },
    },
})

-- Тип 'any' сохраняет вид объекта (W — линия, R — отношение) отдельным столбцом.
tables.areas = osm2pgsql.define_table({
    name = 'areas',
    ids = { type = 'any', type_column = 'osm_type', id_column = 'osm_id' },
    columns = {
        { column = 'kind', type = 'text', not_null = true },
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'multipolygon', projection = 4326, not_null = true },
    },
})

tables.boundaries = osm2pgsql.define_table({
    name = 'boundaries',
    ids = { type = 'relation', id_column = 'osm_id' },
    columns = {
        { column = 'admin_level', type = 'int', not_null = true },
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'multipolygon', projection = 4326, not_null = true },
    },
})

tables.routes = osm2pgsql.define_table({
    name = 'routes',
    ids = { type = 'relation', id_column = 'osm_id' },
    columns = {
        { column = 'tags', type = 'jsonb' },
        { column = 'geom', type = 'multilinestring', projection = 4326, not_null = true },
    },
})

-- Классы дорог сети. service — кроме парковочных проездов и въездов во дворы.
local road_classes = {
    motorway = 'motorway', motorway_link = 'motorway',
    trunk = 'trunk', trunk_link = 'trunk',
    primary = 'primary', primary_link = 'primary',
    secondary = 'secondary', secondary_link = 'secondary',
    tertiary = 'tertiary', tertiary_link = 'tertiary',
    unclassified = 'unclassified', residential = 'residential',
    living_street = 'living_street', service = 'service', track = 'track',
}

local skip_service = { parking_aisle = true, driveway = true, ['drive-through'] = true, emergency_access = true }

-- Здания, которые могут быть складами.
local storage_buildings = {
    warehouse = true, industrial = true, hangar = true, depot = true, logistics = true,
    storage = true, cold_storage = true, transportation = true,
}

-- Кольца Москвы: дорожные маршруты по названию или номеру.
local ring_names = {
    ['Садовое кольцо'] = true,
    ['Третье транспортное кольцо'] = true,
    ['Московская кольцевая автомобильная дорога'] = true,
    ['МКАД'] = true,
    ['Центральная кольцевая автомобильная дорога'] = true,
    ['ЦКАД'] = true,
}
local ring_refs = { ['МКАД'] = true, ['ТТК'] = true, ['ЦКАД'] = true, ['А-113'] = true, ['A-113'] = true }

-- Есть ли в строке одна из подстрок. string.lower не меняет кириллицу, поэтому варианты
-- с заглавной буквой перечисляются явно.
local function contains(s, ...)
    if not s then
        return false
    end
    for _, p in ipairs({ ... }) do
        if s:find(p, 1, true) then
            return true
        end
    end
    return false
end

-- Признак грузовой стоянки: явный доступ грузовиков или название.
local function is_truck_parking(t)
    if t.amenity ~= 'parking' then
        return false
    end
    if t.hgv == 'yes' or t.hgv == 'designated' or t.parking == 'truck' then
        return true
    end
    return contains(t.name, 'грузов', 'Грузов', 'фур', 'Фур', 'большегруз', 'Большегруз')
end

-- Ж/д объекты грузового назначения.
local function is_rail_freight(t)
    if t.railway ~= 'station' and t.railway ~= 'halt' and t.railway ~= 'yard' then
        return false
    end
    return t.railway == 'yard' or t.usage == 'freight' or t['railway:traffic_mode'] == 'freight'
        or t['railway:traffic_mode'] == 'mixed' or t.cargo ~= nil or t.station == 'freight'
end

-- Вид точечного или площадного объекта; nil — не нужен.
local function poi_kind(t)
    if t.amenity == 'fuel' then return 'fuel' end
    if is_truck_parking(t) then return 'truck_parking' end
    if t.highway == 'rest_area' or t.highway == 'services' then return 'rest_area' end
    if t.amenity == 'marketplace' then return 'marketplace' end
    if t.amenity == 'post_depot' then return 'post_depot' end
    if t.shop == 'wholesale' then return 'wholesale' end
    if t.office == 'customs' or t.amenity == 'customs' or t.government == 'customs' then return 'customs' end
    if is_rail_freight(t) then return 'rail_freight' end
    -- Вокзалы: станция или здание вокзала с этим словом в названии.
    if (t.railway == 'station' or t.building == 'train_station' or t.public_transport == 'station')
        and contains(t.name, 'вокзал', 'Вокзал') then
        return 'rail_station'
    end
    if t.industrial == 'warehouse' or t.industrial == 'logistics' or t.industrial == 'distributor'
        or t.office == 'logistics' then
        return 'logistics'
    end
    return nil
end

-- Вид площадного объекта.
local function area_kind(t)
    local k = poi_kind(t)
    if k then return k end
    if t.building and (storage_buildings[t.building] or t['building:use'] == 'warehouse'
        or t['building:use'] == 'industrial') then
        return 'building'
    end
    if t.landuse == 'industrial' then return 'industrial' end
    if t.landuse == 'railway' then return 'railway_land' end
    if t.landuse == 'port' or t.harbour == 'yes' or t.industrial == 'port' then return 'port' end
    if t.landuse == 'residential' then return 'residential' end
    if t.leisure == 'park' or t.leisure == 'nature_reserve' or t.boundary == 'protected_area'
        or t.boundary == 'national_park' then
        return 'green'
    end
    if t.man_made == 'bridge' then return 'bridge' end
    return nil
end

function osm2pgsql.process_node(object)
    local kind = poi_kind(object.tags)
    if kind then
        tables.points:insert({ kind = kind, tags = object.tags, geom = object:as_point() })
    end
end

function osm2pgsql.process_way(object)
    local t = object.tags
    local class = road_classes[t.highway]
    if class and not (class == 'service' and skip_service[t.service]) and t.area ~= 'yes' then
        tables.roads:insert({ class = class, tags = t, geom = object:as_linestring() })
    end

    if (t.railway == 'rail' and (t.service == 'siding' or t.service == 'spur' or t.service == 'yard'
        or t.usage == 'industrial')) then
        tables.rails:insert({ kind = 'freight_track', tags = t, geom = object:as_linestring() })
    end

    if object.is_closed then
        local kind = area_kind(t)
        if kind then
            tables.areas:insert({ kind = kind, tags = t, geom = object:as_polygon() })
        end
    end
end

function osm2pgsql.process_relation(object)
    local t = object.tags

    if t.type == 'boundary' and t.boundary == 'administrative' then
        local level = tonumber(t.admin_level)
        if level == 4 or level == 5 or level == 6 or level == 8 then
            tables.boundaries:insert({ admin_level = level, tags = t, geom = object:as_multipolygon() })
        end
        return
    end

    if t.type == 'route' and t.route == 'road' and (ring_names[t.name] or ring_refs[t.ref]) then
        tables.routes:insert({ tags = t, geom = object:as_multilinestring() })
        return
    end

    if t.type == 'multipolygon' then
        local kind = area_kind(t)
        if kind then
            tables.areas:insert({ kind = kind, tags = t, geom = object:as_multipolygon() })
        end
    end
end
