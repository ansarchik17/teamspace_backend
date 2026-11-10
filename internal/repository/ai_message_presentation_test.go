package repository

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/west6ide/workspace-backend/internal/alem"
)

func TestAIMessageCardHistoryJSONBRoundTrip(t *testing.T) {
	codec := pgtype.NewMap()
	for _, cards := range [][]alem.DataCard{nil, {}, {{ID: "S1-C1", SourceID: "S1", Title: "Договор", Service: "ЭДО", Href: "/web/eps?document=doc-1", Fields: []alem.DataCardField{{Label: "Номер", Value: "42"}}}}} {
		encoded, err := codec.Encode(pgtype.JSONBOID, pgtype.TextFormatCode, cards, nil)
		if err != nil {
			t.Fatal(err)
		}
		var restored []alem.DataCard
		if err := codec.Scan(pgtype.JSONBOID, pgtype.TextFormatCode, encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cards, restored) {
			t.Fatalf("history lost card metadata: %#v %#v", cards, restored)
		}
	}
}
