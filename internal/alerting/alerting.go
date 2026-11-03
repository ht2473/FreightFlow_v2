// Package alerting — правила оповещений и алерты: что отслеживается (работы, ограничения,
// доступность, новые объекты, состояние источников), проверка по событиям и расписанию с
// дедупликацией и жизненный цикл алерта (активен → подтверждён → закрыт) с журналом.
package alerting

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Виды правил.
const (
	KindRoadworks     = "roadworks"     // новые и изменённые дорожные работы в области
	KindRestrictions  = "restrictions"  // изменения ограничений для грузовиков в области
	KindAccessibility = "accessibility" // ухудшение доступности объектов
	KindFacilities    = "facilities"    // новые объекты реестра
	KindSources       = "sources"       // сбой и устаревание источников данных
)

// Kinds — виды правил в порядке показа.
var Kinds = []string{KindRoadworks, KindRestrictions, KindAccessibility, KindFacilities, KindSources}

// Способы задания области правила.
const (
	ScopeCorridor   = "corridor"   // линия с буфером
	ScopeFacilities = "facilities" // объекты реестра с радиусом
	ScopeArea       = "area"       // многоугольник
	ScopeCity       = "city"       // весь охват данных
	ScopeNone       = "none"       // область не нужна (источники)
)

// kindScopes — допустимые способы задания области для вида.
var kindScopes = map[string][]string{
	KindRoadworks:     {ScopeCorridor, ScopeFacilities, ScopeArea},
	KindRestrictions:  {ScopeCorridor, ScopeFacilities, ScopeArea},
	KindAccessibility: {ScopeFacilities},
	KindFacilities:    {ScopeArea, ScopeCity},
	KindSources:       {ScopeNone},
}

// Важность.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

var severities = []string{SeverityInfo, SeverityWarning, SeverityCritical}

// maxSeverity — более высокая из двух важностей.
func maxSeverity(a, b string) string {
	if slices.Index(severities, b) > slices.Index(severities, a) {
		return b
	}
	return a
}

// Каналы доставки.
const (
	ChannelInApp    = "in_app"
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
	ChannelWebhook  = "webhook"
)

var channels = []string{ChannelInApp, ChannelEmail, ChannelTelegram, ChannelWebhook}

// Статусы алерта.
const (
	StatusActive       = "active"
	StatusAcknowledged = "acknowledged"
	StatusClosed       = "closed"
)

// Справочники параметров.
var (
	workKinds        = []string{"repair", "emergency", "closure"}
	workChanges      = []string{"appeared", "changed", "started"}
	restrictionKinds = []string{
		"hgv_ban", "motor_vehicle_ban", "hgv_destination", "maxheight", "maxweight",
		"maxaxleload", "maxwidth", "maxlength", "hazmat_ban",
	}
	accessTargets = []string{"mkad", "ttk", "center", "rail_freight"}
	facilityTypes = []string{
		"warehouse", "logistics_center", "terminal", "sorting_center", "cold_storage",
		"wholesale", "port",
	}
)

// Пределы параметров.
const (
	maxFacilities  = 50
	minBufferM     = 50
	maxBufferM     = 2000
	minRadiusM     = 100
	maxRadiusM     = 5000
	maxVertices    = 2000
	maxAreaKm2     = 5000
	maxCorridorKm  = 300
	maxStaleFactor = 10
)

// Scope — как задана область правила; область вычисляется из неё при сохранении.
type Scope struct {
	Type        string          `json:"type"`
	Line        json.RawMessage `json:"line,omitempty"`    // GeoJSON LineString
	BufferM     int             `json:"bufferM,omitempty"` // ширина коридора в каждую сторону
	FacilityIDs []uuid.UUID     `json:"facilityIds,omitempty"`
	RadiusM     int             `json:"radiusM,omitempty"`
	Polygon     json.RawMessage `json:"polygon,omitempty"` // GeoJSON Polygon или MultiPolygon
}

// Params — параметры вида правила; заполняются поля своего вида.
type Params struct {
	// Работы: виды работ (пусто — все), только с перекрытием, какие изменения сообщать.
	WorkKinds   []string `json:"workKinds,omitempty"`
	ClosureOnly bool     `json:"closureOnly,omitempty"`
	Changes     []string `json:"changes,omitempty"`
	// Ограничения: виды (пусто — все).
	RestrictionKinds []string `json:"restrictionKinds,omitempty"`
	// Доступность: профиль ТС, цель и прирост времени, о котором сообщать.
	Profile      string  `json:"profile,omitempty"`
	Target       string  `json:"target,omitempty"`
	ThresholdMin float64 `json:"thresholdMin,omitempty"`
	// Новые объекты: типы (пусто — все).
	FacilityTypes []string `json:"facilityTypes,omitempty"`
	// Источники: коды (пусто — все) и во сколько раз превышен интервал обновления.
	Sources     []string `json:"sources,omitempty"`
	StaleFactor float64  `json:"staleFactor,omitempty"`
}

// RuleInput — правило, как его задаёт пользователь.
type RuleInput struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Scope    Scope    `json:"scope"`
	Params   Params   `json:"params"`
	Severity string   `json:"severity"`
	Channels []string `json:"channels"`
	Enabled  bool     `json:"enabled"`
}

// Rule — сохранённое правило.
type Rule struct {
	RuleInput
	ID         uuid.UUID       `json:"id"`
	OwnerID    uuid.UUID       `json:"ownerId"`
	OwnerName  string          `json:"ownerName"`
	Area       json.RawMessage `json:"area,omitempty"` // GeoJSON вычисленной области
	CheckedAt  *time.Time      `json:"checkedAt,omitempty"`
	CheckError *string         `json:"checkError,omitempty"`
	OpenAlerts int             `json:"openAlerts"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// Alert — алерт.
type Alert struct {
	ID             uuid.UUID       `json:"id"`
	RuleID         uuid.UUID       `json:"ruleId"`
	RuleName       string          `json:"ruleName"`
	Kind           string          `json:"kind"`
	OwnerID        uuid.UUID       `json:"ownerId"`
	Entity         string          `json:"entity"`
	Severity       string          `json:"severity"`
	Status         string          `json:"status"`
	Title          string          `json:"title"`
	Summary        string          `json:"summary"`
	Details        json.RawMessage `json:"details"`
	Geometry       json.RawMessage `json:"geometry,omitempty"`
	Occurrences    int             `json:"occurrences"`
	AssigneeID     *uuid.UUID      `json:"assigneeId,omitempty"`
	AssigneeName   *string         `json:"assigneeName,omitempty"`
	AcknowledgedAt *time.Time      `json:"acknowledgedAt,omitempty"`
	ClosedAt       *time.Time      `json:"closedAt,omitempty"`
	Resolution     *string         `json:"resolution,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	LastSeenAt     time.Time       `json:"lastSeenAt"`
}

// LogEntry — запись журнала алерта.
type LogEntry struct {
	ID        int64           `json:"id"`
	At        time.Time       `json:"at"`
	ActorID   *uuid.UUID      `json:"actorId,omitempty"`
	ActorName *string         `json:"actorName,omitempty"`
	Action    string          `json:"action"`
	Comment   *string         `json:"comment,omitempty"`
	Details   json.RawMessage `json:"details"`
}

// Ошибки модуля.
var (
	ErrNotFound  = errors.New("alerting: not found")
	ErrForbidden = errors.New("alerting: forbidden")
)

// ErrInvalid — неверные параметры; текст — для пользователя.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return "alerting: " + e.Reason }

func invalid(format string, a ...any) error { return &ErrInvalid{Reason: fmt.Sprintf(format, a...)} }

func subset(vals, allowed []string, what string) error {
	for _, v := range vals {
		if !slices.Contains(allowed, v) {
			return invalid("%s: неизвестное значение %q", what, v)
		}
	}
	return nil
}

// Validate проверяет правило и приводит его к каноническому виду: параметры чужих видов
// сбрасываются, значения по умолчанию подставляются.
func (in *RuleInput) Validate() error {
	if n := len([]rune(in.Name)); n == 0 || n > 200 {
		return invalid("название — от 1 до 200 символов")
	}
	scopes, ok := kindScopes[in.Kind]
	if !ok {
		return invalid("неизвестный вид правила %q", in.Kind)
	}
	if in.Kind == KindSources && in.Scope.Type == "" {
		in.Scope.Type = ScopeNone
	}
	if !slices.Contains(scopes, in.Scope.Type) {
		return invalid("для этого вида правила область задаётся иначе")
	}
	if in.Severity == "" {
		in.Severity = SeverityWarning
	}
	if !slices.Contains(severities, in.Severity) {
		return invalid("неизвестная важность %q", in.Severity)
	}
	if len(in.Channels) == 0 {
		in.Channels = []string{ChannelInApp}
	}
	if err := subset(in.Channels, channels, "каналы"); err != nil {
		return err
	}
	in.Channels = sortedSet(in.Channels)
	if err := in.Scope.validate(); err != nil {
		return err
	}
	return in.validateParams()
}

func (s *Scope) validate() error {
	keep := Scope{Type: s.Type}
	switch s.Type {
	case ScopeCorridor:
		if len(s.Line) == 0 {
			return invalid("не задана линия коридора")
		}
		if s.BufferM == 0 {
			s.BufferM = 200
		}
		if s.BufferM < minBufferM || s.BufferM > maxBufferM {
			return invalid("ширина коридора — от %d до %d м", minBufferM, maxBufferM)
		}
		keep.Line, keep.BufferM = s.Line, s.BufferM
	case ScopeFacilities:
		ids := slices.Clone(s.FacilityIDs)
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		ids = slices.Compact(ids)
		if len(ids) == 0 || len(ids) > maxFacilities {
			return invalid("объектов — от 1 до %d", maxFacilities)
		}
		if s.RadiusM == 0 {
			s.RadiusM = 500
		}
		if s.RadiusM < minRadiusM || s.RadiusM > maxRadiusM {
			return invalid("радиус вокруг объекта — от %d до %d м", minRadiusM, maxRadiusM)
		}
		keep.FacilityIDs, keep.RadiusM = ids, s.RadiusM
	case ScopeArea:
		if len(s.Polygon) == 0 {
			return invalid("не задана область")
		}
		keep.Polygon = s.Polygon
	}
	*s = keep
	return nil
}

func (in *RuleInput) validateParams() error {
	p := in.Params
	keep := Params{}
	switch in.Kind {
	case KindRoadworks:
		if err := subset(p.WorkKinds, workKinds, "виды работ"); err != nil {
			return err
		}
		if len(p.Changes) == 0 {
			p.Changes = []string{"appeared", "started"}
		}
		if err := subset(p.Changes, workChanges, "изменения работ"); err != nil {
			return err
		}
		keep.WorkKinds, keep.ClosureOnly, keep.Changes = sortedSet(p.WorkKinds), p.ClosureOnly, sortedSet(p.Changes)
	case KindRestrictions:
		if err := subset(p.RestrictionKinds, restrictionKinds, "виды ограничений"); err != nil {
			return err
		}
		keep.RestrictionKinds = sortedSet(p.RestrictionKinds)
	case KindAccessibility:
		if p.Profile == "" {
			return invalid("не выбран профиль ТС")
		}
		if p.Target == "" {
			p.Target = "mkad"
		}
		if !slices.Contains(accessTargets, p.Target) {
			return invalid("неизвестная цель доступности %q", p.Target)
		}
		if p.ThresholdMin == 0 {
			p.ThresholdMin = 5
		}
		if p.ThresholdMin < 1 || p.ThresholdMin > 120 {
			return invalid("прирост времени — от 1 до 120 мин")
		}
		keep.Profile, keep.Target, keep.ThresholdMin = p.Profile, p.Target, p.ThresholdMin
	case KindFacilities:
		if err := subset(p.FacilityTypes, facilityTypes, "типы объектов"); err != nil {
			return err
		}
		keep.FacilityTypes = sortedSet(p.FacilityTypes)
	case KindSources:
		if p.StaleFactor == 0 {
			p.StaleFactor = 2
		}
		if p.StaleFactor < 1.5 || p.StaleFactor > maxStaleFactor {
			return invalid("порог устаревания — от 1,5 до %d интервалов обновления", maxStaleFactor)
		}
		keep.Sources, keep.StaleFactor = sortedSet(p.Sources), p.StaleFactor
	}
	in.Params = keep
	return nil
}

func sortedSet(v []string) []string {
	if len(v) == 0 {
		return nil
	}
	out := slices.Clone(v)
	slices.Sort(out)
	return slices.Compact(out)
}
