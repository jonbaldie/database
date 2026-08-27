package mysql

import (
	"reflect"
	"testing"
)

func TestWindowPartitionEvaluationMatchesRowEvaluation(t *testing.T) {
	plan := &relationalSelectPlan{source: relationalSource{columns: []relationColumn{{name: "value", typeName: "INT", index: 0}}}}
	rows := []relationalResultRow{
		{source: relationRow{values: []string{storedSQLNullValue}}},
		{source: relationRow{values: []string{"1"}}},
		{source: relationRow{values: []string{"2"}}},
		{source: relationRow{values: []string{"2"}}},
		{source: relationRow{values: []string{"5"}}},
	}
	ascending := []int{0, 1, 2, 3, 4}
	descending := []int{4, 2, 3, 1, 0}
	ascOrder := []relationalWindowOrder{{expression: "value", direction: "ASC"}}
	descOrder := []relationalWindowOrder{{expression: "value", direction: "DESC"}}
	whole := relationalWindowFrame{present: true, mode: "rows", start: relationalWindowBound{kind: "unbounded_preceding"}, end: relationalWindowBound{kind: "unbounded_following"}}
	bounded := relationalWindowFrame{present: true, mode: "rows", start: relationalWindowBound{kind: "preceding", offset: 2}, end: relationalWindowBound{kind: "preceding", offset: 1}}
	peers := relationalWindowFrame{present: true, mode: "range", start: relationalWindowBound{kind: "current_row"}, end: relationalWindowBound{kind: "current_row"}}
	numeric := relationalWindowFrame{present: true, mode: "range", start: relationalWindowBound{kind: "preceding", offset: 2}, end: relationalWindowBound{kind: "current_row"}}

	cases := []struct {
		name      string
		partition []int
		function  relationalWindowFunction
	}{
		{name: "rank peers", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "RANK"}, spec: relationalWindowSpec{order: ascOrder}}},
		{name: "dense rank peers", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "DENSE_RANK"}, spec: relationalWindowSpec{order: ascOrder}}},
		{name: "count whole", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "COUNT", argument: "*"}, spec: relationalWindowSpec{order: ascOrder, frame: whole}}},
		{name: "sum bounded and empty", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "SUM", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: bounded}}},
		{name: "average bounded and empty", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "AVG", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: bounded}}},
		{name: "minimum peer range", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "MIN", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: peers}}},
		{name: "maximum numeric range", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "MAX", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: numeric}}},
		{name: "descending numeric range", partition: descending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "SUM", argument: "value"}, spec: relationalWindowSpec{order: descOrder, frame: numeric}}},
		{name: "first bounded", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "FIRST_VALUE", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: bounded}}},
		{name: "last peers", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "LAST_VALUE", argument: "value"}, spec: relationalWindowSpec{order: ascOrder, frame: peers}}},
		{name: "nth numeric", partition: ascending, function: relationalWindowFunction{relationalAggregate: relationalAggregate{name: "NTH_VALUE", argument: "value, 2"}, spec: relationalWindowSpec{order: ascOrder, frame: numeric}}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := plan.windowPartitionValues(rows, test.partition, test.function)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]exprValue, len(test.partition))
			for position := range test.partition {
				want[position], err = plan.windowValue(rows, test.partition, position, test.function)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("partition values = %#v, want %#v", got, want)
			}
		})
	}
}
