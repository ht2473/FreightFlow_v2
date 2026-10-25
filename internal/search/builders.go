package search

// Builder — построитель документов одного вида. Signature — дешёвый запрос, значение которого
// меняется вместе с исходными данными; Build — вставка всех документов вида (старые удаляются
// в той же транзакции) с параметрами Args.
type Builder struct {
	Kind      string
	Signature string
	Build     string
	Args      func() []any
}

// version — версия домена данных в подписи.
func version(domain string) string {
	return `(SELECT coalesce(max(version), 0)::text FROM ingest.data_version WHERE domain = '` + domain + `')`
}

// Builders — построители по видам. Справка и отчёты подключаются вместе с разделами (Б10).
var Builders = []Builder{
	{
		// Объекты реестра: название (уточнённое редактором), адрес, тип с синонимами, округ и район
		// полностью и сокращённо. Важность — по площади застройки.
		Kind: "facility",
		Signature: `SELECT ` + version("facility") + ` || '|' || count(*) || '|' || coalesce(max(updated_at)::text, '')
			FROM infra.facility`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, point, weight)
			SELECT 'facility', f.id::text, coalesce(f.name, t.name),
			       coalesce(f.official_address, f.address, t.name),
			       concat_ws(' ', t.name, $1::jsonb ->> f.type_code, o.name, o.short_name, d.name,
			                 f.official_address, f.address),
			       f.point,
			       least(1, 0.45 + coalesce(ln(1 + f.building_area_m2) / 30, 0))::real
			FROM infra.facility f
			JOIN ref.facility_type t ON t.code = f.type_code
			LEFT JOIN territory.admin_area o ON o.id = f.okrug_id
			LEFT JOIN territory.admin_area d ON d.id = f.district_id
			WHERE f.status = 'active'`,
		// $1 — дополнительные слова по типу объекта.
		Args: func() []any { return []any{typeSynonyms} },
	},
	{
		// Адреса реестра Москвы: здания и сооружения важнее земельных участков.
		Kind:      "address",
		Signature: `SELECT ` + version("territory") + ` || '|' || (SELECT count(*) FROM territory.address)`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, point, weight)
			SELECT 'address', a.unom::text, coalesce(a.simple_address, a.address),
			       concat_ws(' · ', a.obj_type, replace(a.adm_area, ' административный округ', ''),
			                 replace(a.municipal, 'муниципальный округ ', '')),
			       concat_ws(' ', a.adm_area, a.municipal, a.settlement),
			       a.point,
			       CASE a.obj_type WHEN 'здание' THEN 0.4 WHEN 'сооружение' THEN 0.35 ELSE 0.25 END
			FROM territory.address a
			WHERE a.point IS NOT NULL`,
	},
	{
		// Улицы — по адресному реестру: точка — середина адресов улицы, подпись — округа.
		Kind:      "street",
		Signature: `SELECT ` + version("territory") + ` || '|' || (SELECT count(*) FROM territory.address)`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, point, weight)
			SELECT 'street', core.normalize_text(a.street), min(a.street),
			       string_agg(DISTINCT replace(a.adm_area, ' административный округ', ''), ', '),
			       string_agg(DISTINCT replace(a.municipal, 'муниципальный округ ', ''), ' '),
			       ST_PointOnSurface(ST_Collect(a.point)),
			       least(0.85, 0.5 + ln(1 + count(*)) / 40)::real
			FROM territory.address a
			WHERE a.street IS NOT NULL AND a.point IS NOT NULL
			GROUP BY core.normalize_text(a.street)`,
	},
	{
		// Округа и районы Москвы: полное и краткое название, у района — «район» без слова.
		Kind:      "territory",
		Signature: `SELECT ` + version("territory"),
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, point, weight)
			SELECT 'territory', a.id::text, a.name,
			       CASE a.kind WHEN 'okrug' THEN 'Административный округ'
			                   ELSE 'Район · ' || coalesce(p.short_name, p.name, '') END,
			       concat_ws(' ', a.short_name, regexp_replace(a.name, '^район |район$', '', 'g'),
			                 replace(a.name, ' административный округ', ' округ'), p.name, p.short_name),
			       ST_PointOnSurface(a.geom),
			       CASE a.kind WHEN 'okrug' THEN 0.95 ELSE 0.85 END
			FROM territory.admin_area a
			LEFT JOIN territory.admin_area p ON p.id = a.parent_id
			WHERE a.kind IN ('okrug', 'district')`,
	},
	{
		// Наборы данных портала Москвы и прочие источники.
		Kind:      "dataset",
		Signature: `SELECT count(*) || '|' || coalesce(max(synced_at)::text, '') FROM ingest.source WHERE code LIKE 'mos\_%'`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, weight)
			SELECT 'dataset', s.code, s.title, s.publisher, concat_ws(' ', 'набор данных', s.attribution, s.license, s.code), 0.6
			FROM ingest.source s WHERE s.code LIKE 'mos\_%' AND s.registered`,
	},
	{
		Kind:      "source",
		Signature: `SELECT count(*) || '|' || coalesce(max(synced_at)::text, '') FROM ingest.source WHERE code NOT LIKE 'mos\_%'`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, weight)
			SELECT 'source', s.code, s.title, s.publisher, concat_ws(' ', 'источник данных', s.attribution, s.license, s.code), 0.6
			FROM ingest.source s WHERE s.code NOT LIKE 'mos\_%' AND s.registered`,
	},
	{
		// Правовые акты: реквизиты, краткое содержание, текст официальной страницы.
		Kind: "legal_act",
		Signature: `SELECT ` + version("regulation") + ` || '|' || (SELECT count(*) || coalesce(max(verified_on)::text, '')
			FROM ref.legal_document) || '|' || (SELECT coalesce(max(changed_at)::text, '') FROM regulation.official_page)`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, weight)
			SELECT 'legal_act', d.code, d.title,
			       concat_ws(' ', d.authority, CASE WHEN d.number IS NOT NULL THEN '№ ' || d.number END,
			                 CASE WHEN d.signed_on IS NOT NULL THEN 'от ' || to_char(d.signed_on, 'DD.MM.YYYY') END),
			       concat_ws(' ', d.kind, d.number, d.summary, p.content),
			       CASE d.status WHEN 'active' THEN 0.7 ELSE 0.5 END
			FROM ref.legal_document d
			LEFT JOIN regulation.official_page p ON p.code = d.code`,
	},
	{
		// Идущие и запланированные дорожные работы.
		Kind:      "road_work",
		Signature: `SELECT ` + version("operations") + ` || '|' || current_date`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, point, weight)
			SELECT 'road_work', w.id::text, w.title,
			       concat_ws(' · ', CASE w.state WHEN 'active' THEN 'идут' ELSE 'запланированы' END, w.place, w.district),
			       concat_ws(' ', 'дорожные работы', w.kind, w.adm_area, w.customer, w.contractor,
			                 CASE WHEN w.closure THEN 'перекрытие' END),
			       ST_PointOnSurface(w.geom),
			       CASE w.state WHEN 'active' THEN 0.6 ELSE 0.45 END
			FROM operations.road_work w
			WHERE w.state IN ('planned', 'active') AND w.removed_at IS NULL AND w.geom IS NOT NULL`,
	},
	{
		// Сохранённые виды: видны владельцу и, у общих, обладателям роли.
		Kind:      "saved_view",
		Signature: `SELECT count(*) || '|' || coalesce(max(created_at)::text, '') || '|' || coalesce(sum(hashtext(id::text)), 0) FROM core.saved_view`,
		Build: `INSERT INTO search.document (kind, ref, title, subtitle, body, weight, owner_id, role_code)
			SELECT 'saved_view', v.id::text, v.name,
			       CASE v.page WHEN 'map' THEN 'Вид карты' ELSE 'Вид реестра объектов' END,
			       concat_ws(' ', 'сохранённый вид', v.params::text), 0.7, v.owner_id, v.role_code
			FROM core.saved_view v`,
	},
}

// builder — построитель по виду.
func builder(kind string) (Builder, bool) {
	for _, b := range Builders {
		if b.Kind == kind {
			return b, true
		}
	}
	return Builder{}, false
}

// AllKinds — все виды документов индекса.
func AllKinds() []string {
	out := make([]string, len(Builders))
	for i, b := range Builders {
		out[i] = b.Kind
	}
	return out
}
