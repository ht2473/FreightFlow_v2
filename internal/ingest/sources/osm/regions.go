package osm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Многоугольники вырезки из выгрузки ЦФО (etl/regions/*.geojson), строятся из границ OSM
// командой «etl regions» после первой загрузки и хранятся в репозитории. Буфер считается в
// UTM 37N, контур упрощается с запасом наружу.
var regionQueries = map[string]string{
	// Москва и агломерация внутри ЦКАД с буфером 5 км — объекты, сеть, территории.
	"city": `
		SELECT ST_AsGeoJSON(ST_ReducePrecision(ST_Transform(ST_Buffer(ST_SimplifyPreserveTopology(
			ST_Buffer(ST_Transform(ST_Union(geom), 32637), 5000), 200), 0), 4326), 0.00001), 5)
		FROM (
			SELECT geom FROM territory.admin_area WHERE kind = 'region' AND name = 'Москва'
			UNION ALL
			SELECT zone FROM territory.ring_zone WHERE code = 'ckad' AND zone IS NOT NULL
		) s`,
	// Москва и Московская область с буфером 20 км — граф маршрутизации.
	"routing": `
		SELECT ST_AsGeoJSON(ST_ReducePrecision(ST_Transform(ST_Buffer(ST_SimplifyPreserveTopology(
			ST_Buffer(ST_Transform(ST_Union(geom), 32637), 20000), 500), 0), 4326), 0.00001), 5)
		FROM territory.admin_area WHERE kind = 'region'`,
}

// ExportRegions записывает многоугольники вырезки в каталог dir.
func ExportRegions(ctx context.Context, pool *pgxpool.Pool, dir string) ([]string, error) {
	var written []string
	for _, name := range []string{"city", "routing"} {
		var geom *string
		if err := pool.QueryRow(ctx, regionQueries[name]).Scan(&geom); err != nil {
			return nil, fmt.Errorf("region %s: %w", name, err)
		}
		if geom == nil {
			return nil, fmt.Errorf("region %s: no boundaries loaded (run etl run osm first)", name)
		}
		doc := fmt.Sprintf(`{"type":"Feature","properties":{"name":%q},"geometry":%s}`+"\n", name, *geom)
		path := filepath.Join(dir, name+".geojson")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}
