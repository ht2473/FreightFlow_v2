package api

import (
	"testing"

	"freightflow/internal/scenario"
)

// Новый объект в базе не существует: времени базы нет, и итог сценария всё равно отдаётся
// (null в baseMin не проходит схему ответа, и падал бы весь список сценариев).
func TestToScenarioNewFacilityWithoutBase(t *testing.T) {
	sm := 12.5
	sc := &scenario.Scenario{
		Status: "done",
		Params: scenario.Params{Profile: "truck_20", Targets: []string{"mkad"}},
		Summary: &scenario.Summary{
			Targets:  []scenario.TargetSummary{},
			Closures: []scenario.ClosureInfo{},
			NewFacility: []scenario.EntityTime{
				{Kind: "mkad", ScenarioMin: &sm, BaseStatus: "unreachable", ScenarioStatus: "ok"},
			},
		},
	}
	out, err := toScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	nf := out.Summary.Value.NewFacility
	if len(nf) != 1 || nf[0].BaseMin.Set || nf[0].ScenarioMin.Value != sm {
		t.Fatalf("newFacility = %+v", nf)
	}
}
