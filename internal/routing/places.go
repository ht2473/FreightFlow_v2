package routing

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Place — найденная точка для маршрута: объект реестра или адрес.
type Place struct {
	Kind     string // facility, address
	ID       string
	Title    string
	Subtitle string
	Point    Point
}

// houseToken — номер дома, корпуса или строения в запросе: «10», «12а», «2/1».
var houseToken = regexp.MustCompile(`^[0-9][0-9a-zа-яё/]*$`)

// splitQuery делит запрос на слова (для поиска по сходству) и номера (сверяются целиком).
func splitQuery(q string) (words string, numbers []string) {
	var ws []string
	for _, t := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == ';'
	}) {
		switch {
		case houseToken.MatchString(t):
			numbers = append(numbers, t)
		case len([]rune(t)) >= 2 && strings.IndexFunc(t, unicode.IsLetter) >= 0:
			ws = append(ws, t)
		}
	}
	return strings.Join(ws, " "), numbers
}

// Places ищет объекты реестра по названию и адреса реестра Москвы по словам запроса; адреса с
// номером дома из запроса — выше. Полнотекстовый поиск и геокодер — в разделе поиска.
func (s *Service) Places(ctx context.Context, q string, limit int) ([]Place, error) {
	words, numbers := splitQuery(q)
	if len([]rune(words)) < 3 {
		return []Place{}, nil
	}
	if numbers == nil {
		numbers = []string{}
	}
	rows, err := s.pool.Query(ctx, `
		(SELECT 'facility', f.id::text, f.name, coalesce(f.official_address, f.address, ''), ST_X(f.point), ST_Y(f.point)
		 FROM infra.facility f
		 WHERE f.status = 'active' AND f.name IS NOT NULL AND ($1 <% f.name OR f.name ILIKE '%' || $1 || '%')
		 ORDER BY word_similarity($1, f.name) DESC, length(f.name)
		 LIMIT 3)
		UNION ALL
		(SELECT 'address', a.unom::text, a.simple_address, coalesce(a.obj_type, ''), ST_X(a.point), ST_Y(a.point)
		 FROM territory.address a
		 WHERE a.point IS NOT NULL AND a.simple_address IS NOT NULL AND $1 <% a.simple_address
		 ORDER BY (SELECT count(*) FROM unnest($2::text[]) n
		           WHERE a.simple_address ~* ('(^|[^0-9])' || n || '([^0-9]|$)')) DESC,
		          word_similarity($1, a.simple_address) DESC, a.obj_type = 'здание' DESC, length(a.simple_address)
		 LIMIT $3)`, words, numbers, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Place, error) {
		var p Place
		return p, r.Scan(&p.Kind, &p.ID, &p.Title, &p.Subtitle, &p.Point.Lon, &p.Point.Lat)
	})
}
