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

func TestNumericRangeFramePreservesUnboundedBoundsForNullOrderValues(t *testing.T) {
	plan := &relationalSelectPlan{source: relationalSource{columns: []relationColumn{
		{name: "id", typeName: "INT", index: 0, coalesce: -1},
		{name: "value", typeName: "INT", index: 1, coalesce: -1},
	}}}
	rows := []relationalResultRow{
		{source: relationRow{values: []string{"1", storedSQLNullValue}}},
		{source: relationRow{values: []string{"2", storedSQLNullValue}}},
		{source: relationRow{values: []string{"3", "10"}}},
		{source: relationRow{values: []string{"4", "20"}}},
	}
	descending := []int{3, 2, 0, 1}
	ascending := []int{0, 1, 2, 3}
	cases := []struct {
		name      string
		partition []int
		order     relationalWindowOrder
		frame     relationalWindowFrame
		wantNull  string
	}{
		{
			name:      "descending unbounded preceding",
			partition: descending,
			order:     relationalWindowOrder{expression: "value", direction: "DESC"},
			frame: relationalWindowFrame{present: true, mode: "range",
				start: relationalWindowBound{kind: "unbounded_preceding"},
				end:   relationalWindowBound{kind: "following", offset: 5}},
			wantNull: "10",
		},
		{
			name:      "ascending unbounded following",
			partition: ascending,
			order:     relationalWindowOrder{expression: "value", direction: "ASC"},
			frame: relationalWindowFrame{present: true, mode: "range",
				start: relationalWindowBound{kind: "preceding", offset: 5},
				end:   relationalWindowBound{kind: "unbounded_following"}},
			wantNull: "10",
		},
		{
			name:      "descending bounded offsets",
			partition: descending,
			order:     relationalWindowOrder{expression: "value", direction: "DESC"},
			frame: relationalWindowFrame{present: true, mode: "range",
				start: relationalWindowBound{kind: "preceding", offset: 5},
				end:   relationalWindowBound{kind: "following", offset: 5}},
			wantNull: "3",
		},
		{
			name:      "ascending unbounded preceding",
			partition: ascending,
			order:     relationalWindowOrder{expression: "value", direction: "ASC"},
			frame: relationalWindowFrame{present: true, mode: "range",
				start: relationalWindowBound{kind: "unbounded_preceding"},
				end:   relationalWindowBound{kind: "following", offset: 5}},
			wantNull: "3",
		},
		{
			name:      "descending unbounded following",
			partition: descending,
			order:     relationalWindowOrder{expression: "value", direction: "DESC"},
			frame: relationalWindowFrame{present: true, mode: "range",
				start: relationalWindowBound{kind: "preceding", offset: 5},
				end:   relationalWindowBound{kind: "unbounded_following"}},
			wantNull: "3",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			function := relationalWindowFunction{
				relationalAggregate: relationalAggregate{name: "SUM", argument: "id"},
				spec:                relationalWindowSpec{order: []relationalWindowOrder{test.order}, frame: test.frame},
			}
			partitionValues, err := plan.windowPartitionValues(rows, test.partition, function)
			if err != nil {
				t.Fatal(err)
			}
			for position, rowIndex := range test.partition {
				value, err := plan.windowValue(rows, test.partition, position, function)
				if err != nil {
					t.Fatal(err)
				}
				if rows[rowIndex].source.values[1] == storedSQLNullValue && value.render() != test.wantNull {
					t.Fatalf("position %d value = %s, want %s", position, value.render(), test.wantNull)
				}
				if partitionValues[position].render() != value.render() {
					t.Fatalf("position %d partition value = %s, row value = %s", position, partitionValues[position].render(), value.render())
				}
			}
		})
	}
}
