package search

import (
	"slices"
	"testing"
)

func TestTokensAndAbbreviations(t *testing.T) {
	t.Parallel()
	got := expandAbbreviations(tokens("Ул. Ёлочная, д. 5, стр.2; пр-т Мира, ж/д"))
	want := []string{"улица", "елочная", "дом", "5", "стр.2", "проспект", "мира", "железнодорожный"}
	if !slices.Equal(got, want) {
		t.Errorf("tokens %q, want %q", got, want)
	}
}

func TestTermsGroupSynonyms(t *testing.T) {
	t.Parallel()
	ts := terms(expandAbbreviations(tokens("склады ЮВАО у МКАД юго-восточный административный округ")))
	if len(ts) != 5 {
		t.Fatalf("terms %+v", ts)
	}
	if ts[0].Forms[0] != "склады" || len(ts[0].Forms) != 1 {
		t.Errorf("слово без группы: %+v", ts[0])
	}
	if ts[1].Forms[0] != "ювао" || !slices.Contains(ts[1].Forms, "юго-восточный административный округ") {
		t.Errorf("сокращение округа: %+v", ts[1])
	}
	if !slices.Contains(ts[3].Forms, "московская кольцевая автодорога") {
		t.Errorf("МКАД: %+v", ts[3])
	}
	// Многословная фраза — одним термином.
	if ts[4].Forms[0] != "юго-восточный административный округ" || !slices.Contains(ts[4].Forms, "ювао") {
		t.Errorf("фраза: %+v", ts[4])
	}
}
