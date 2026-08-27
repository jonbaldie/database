package mysql

type windowFrameRange struct {
	start int
	end   int
}

func (frame windowFrameRange) empty() bool {
	return frame.start > frame.end
}

func (p *relationalSelectPlan) windowPartitionValues(rows []relationalResultRow, partition []int, function relationalWindowFunction) ([]exprValue, error) {
	switch function.name {
	case "ROW_NUMBER":
		values := make([]exprValue, len(partition))
		for position := range partition {
			values[position] = intValue(int64(position + 1))
		}
		return values, nil
	case "RANK", "DENSE_RANK":
		return p.windowRankPartitionValues(rows, partition, function)
	case "LAG", "LEAD":
		return p.windowOffsetPartitionValues(rows, partition, function)
	case "FIRST_VALUE", "LAST_VALUE", "NTH_VALUE":
		return p.windowPositionalPartitionValues(rows, partition, function)
	default:
		return p.windowAggregatePartitionValues(rows, partition, function)
	}
}

func (p *relationalSelectPlan) windowRankPartitionValues(rows []relationalResultRow, partition []int, function relationalWindowFunction) ([]exprValue, error) {
	values := make([]exprValue, len(partition))
	if len(partition) == 0 {
		return values, nil
	}
	dense := function.name == "DENSE_RANK"
	rank := int64(1)
	values[0] = intValue(rank)
	length := len(partition)
	for position := 1; position < length; position++ {
		tied, err := p.windowResultRowsTie(rows[partition[position-1]], rows[partition[position]], function.spec)
		if err != nil {
			return nil, err
		}
		if !tied {
			if dense {
				rank++
			} else {
				rank = int64(position + 1)
			}
		}
		values[position] = intValue(rank)
	}
	return values, nil
}

func (p *relationalSelectPlan) windowOffsetPartitionValues(rows []relationalResultRow, partition []int, function relationalWindowFunction) ([]exprValue, error) {
	values := make([]exprValue, len(partition))
	for position := range partition {
		value, err := p.windowOffsetValue(rows, partition, position, function)
		if err != nil {
			return nil, err
		}
		values[position] = value
	}
	return values, nil
}

func (p *relationalSelectPlan) windowPositionalPartitionValues(rows []relationalResultRow, partition []int, function relationalWindowFunction) ([]exprValue, error) {
	arguments := splitCSV(function.argument)
	if len(arguments) == 0 || len(arguments) > 2 || (function.name != "NTH_VALUE" && len(arguments) != 1) {
		return nil, sqlFailure{1064, "42000", "invalid window arguments"}
	}
	frames, err := p.windowFrameRanges(rows, partition, function.spec)
	if err != nil {
		return nil, err
	}
	values := make([]exprValue, len(partition))
	for position, frame := range frames {
		value, err := p.windowPositionalFrameValue(rows, partition, frame, function.name, arguments)
		if err != nil {
			return nil, err
		}
		values[position] = value
	}
	return values, nil
}

func (p *relationalSelectPlan) windowPositionalFrameValue(rows []relationalResultRow, partition []int, frame windowFrameRange, name string, arguments []string) (exprValue, error) {
	if frame.empty() {
		return nullValue(), nil
	}
	target, valid, err := positionalWindowTarget(name, arguments, frame.end-frame.start+1)
	if err != nil || !valid {
		return nullValue(), err
	}
	return p.windowExpressionValue(rows[partition[frame.start+target]], arguments[0])
}

func (p *relationalSelectPlan) windowFrameRanges(rows []relationalResultRow, partition []int, spec relationalWindowSpec) ([]windowFrameRange, error) {
	frame := effectiveWindowFrame(spec)
	if frame.mode != "range" {
		return rowWindowFrameRanges(len(partition), frame), nil
	}
	if len(spec.order) == 0 {
		return repeatedWindowFrameRange(len(partition), windowFrameRange{start: 0, end: len(partition) - 1}), nil
	}
	if rangeFrameHasOffset(frame) {
		if len(spec.order) != 1 {
			return nil, sqlFailure{3587, "HY000", "RANGE frame with value offset requires one ORDER BY expression"}
		}
		return p.numericRangeFrameRanges(rows, partition, spec.order[0], frame)
	}
	return p.peerWindowFrameRanges(rows, partition, spec, frame)
}

func effectiveWindowFrame(spec relationalWindowSpec) relationalWindowFrame {
	if spec.frame.present {
		return spec.frame
	}
	frame := relationalWindowFrame{mode: "rows", start: relationalWindowBound{kind: "unbounded_preceding"}, end: relationalWindowBound{kind: "unbounded_following"}}
	if len(spec.order) > 0 {
		frame.mode = "range"
		frame.end = relationalWindowBound{kind: "current_row"}
	}
	return frame
}

func rowWindowFrameRanges(length int, frame relationalWindowFrame) []windowFrameRange {
	ranges := make([]windowFrameRange, length)
	for position := range length {
		start, end := windowFrameBounds(position, length, frame)
		ranges[position] = windowFrameRange{start: start, end: end}
	}
	return ranges
}

func repeatedWindowFrameRange(length int, frame windowFrameRange) []windowFrameRange {
	ranges := make([]windowFrameRange, length)
	for position := range ranges {
		ranges[position] = frame
	}
	return ranges
}

func (p *relationalSelectPlan) peerWindowFrameRanges(rows []relationalResultRow, partition []int, spec relationalWindowSpec, frame relationalWindowFrame) ([]windowFrameRange, error) {
	peerStarts, peerEnds, err := p.windowPeerBounds(rows, partition, spec)
	if err != nil {
		return nil, err
	}
	ranges := rowWindowFrameRanges(len(partition), frame)
	for position := range ranges {
		if frame.start.kind == "current_row" {
			ranges[position].start = peerStarts[position]
		}
		if frame.end.kind == "current_row" {
			ranges[position].end = peerEnds[position]
		}
	}
	return ranges, nil
}

func (p *relationalSelectPlan) windowPeerBounds(rows []relationalResultRow, partition []int, spec relationalWindowSpec) ([]int, []int, error) {
	starts := make([]int, len(partition))
	ends := make([]int, len(partition))
	length := len(partition)
	for groupStart := 0; groupStart < length; {
		groupEnd := groupStart
		for groupEnd+1 < length {
			tied, err := p.windowResultRowsTie(rows[partition[groupEnd]], rows[partition[groupEnd+1]], spec)
			if err != nil {
				return nil, nil, err
			}
			if !tied {
				break
			}
			groupEnd++
		}
		for position := groupStart; position <= groupEnd; position++ {
			starts[position] = groupStart
			ends[position] = groupEnd
		}
		groupStart = groupEnd + 1
	}
	return starts, ends, nil
}

func (p *relationalSelectPlan) numericRangeFrameRanges(rows []relationalResultRow, partition []int, order relationalWindowOrder, frame relationalWindowFrame) ([]windowFrameRange, error) {
	orderValues := make([]exprValue, len(partition))
	for position, rowIndex := range partition {
		value, err := p.windowExpressionValue(rows[rowIndex], order.expression)
		if err != nil {
			return nil, sqlFailure{3587, "HY000", "RANGE frame with value offset requires numeric ORDER BY values"}
		}
		orderValues[position] = value
	}
	peerStarts, peerEnds := windowValuePeerBounds(orderValues, order, p)
	nonNullStart, nonNullEnd := windowNonNullBounds(orderValues)
	ranges := make([]windowFrameRange, len(partition))
	for position, current := range orderValues {
		calculated, err := numericRangeFrameRange(orderValues, position, current, peerStarts, peerEnds, nonNullStart, nonNullEnd, order, frame)
		if err != nil {
			return nil, err
		}
		ranges[position] = calculated
	}
	return ranges, nil
}

func numericRangeFrameRange(values []exprValue, position int, current exprValue, peerStarts, peerEnds []int, nonNullStart, nonNullEnd int, order relationalWindowOrder, frame relationalWindowFrame) (windowFrameRange, error) {
	if current.isNull() {
		return windowFrameRange{start: peerStarts[position], end: peerEnds[position]}, nil
	}
	lower, lowerOpen, err := rangeFrameBoundary(current, frame.start, order.direction, true)
	if err != nil {
		return windowFrameRange{}, err
	}
	upper, upperOpen, err := rangeFrameBoundary(current, frame.end, order.direction, false)
	if err != nil {
		return windowFrameRange{}, err
	}
	start, end := nonNullStart, nonNullEnd
	if !lowerOpen {
		start, err = windowRangeLowerBound(values, nonNullStart, nonNullEnd+1, lower, order.direction)
	}
	if err != nil {
		return windowFrameRange{}, err
	}
	if !upperOpen {
		upperEnd, upperErr := windowRangeUpperBound(values, nonNullStart, nonNullEnd+1, upper, order.direction)
		if upperErr != nil {
			return windowFrameRange{}, upperErr
		}
		end = upperEnd - 1
	}
	return windowFrameRange{start: start, end: end}, nil
}

func windowValuePeerBounds(values []exprValue, order relationalWindowOrder, plan *relationalSelectPlan) ([]int, []int) {
	starts := make([]int, len(values))
	ends := make([]int, len(values))
	length := len(values)
	for groupStart := 0; groupStart < length; {
		groupEnd := groupStart
		for groupEnd+1 < length && plan.compareWindowOrderValues(order, values[groupEnd], values[groupEnd+1]) == 0 {
			groupEnd++
		}
		for position := groupStart; position <= groupEnd; position++ {
			starts[position] = groupStart
			ends[position] = groupEnd
		}
		groupStart = groupEnd + 1
	}
	return starts, ends
}

func windowNonNullBounds(values []exprValue) (int, int) {
	start := 0
	length := len(values)
	for start < length && values[start].isNull() {
		start++
	}
	end := length - 1
	for end >= 0 && values[end].isNull() {
		end--
	}
	return start, end
}

func windowRangeLowerBound(values []exprValue, low, high int, boundary exprValue, direction string) (int, error) {
	for low < high {
		middle := low + (high-low)/2
		comparison, err := compareWindowRangeValue(values[middle], boundary, direction)
		if err != nil {
			return 0, err
		}
		if comparison < 0 {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, nil
}

func windowRangeUpperBound(values []exprValue, low, high int, boundary exprValue, direction string) (int, error) {
	for low < high {
		middle := low + (high-low)/2
		comparison, err := compareWindowRangeValue(values[middle], boundary, direction)
		if err != nil {
			return 0, err
		}
		if comparison <= 0 {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, nil
}

type windowAggregateInput struct {
	value   exprValue
	key     string
	present bool
}

type rollingWindowAggregate struct {
	aggregate   relationalAggregate
	kind        rollingAggregateKind
	inputs      []windowAggregateInput
	frequencies map[string]int
	count       int
	total       exprValue
	extremes    []int
	extremeHead int
}

type rollingAggregateKind byte

const (
	rollingCount rollingAggregateKind = iota
	rollingSum
	rollingAverage
	rollingMinimum
	rollingMaximum
)

func rollingAggregateKindFor(name string) rollingAggregateKind {
	switch name {
	case "COUNT":
		return rollingCount
	case "AVG":
		return rollingAverage
	case "MIN":
		return rollingMinimum
	case "MAX":
		return rollingMaximum
	default:
		return rollingSum
	}
}

func (kind rollingAggregateKind) usesExtremes() bool {
	return kind == rollingMinimum || kind == rollingMaximum
}

func (kind rollingAggregateKind) tracksTotal() bool {
	return kind == rollingSum || kind == rollingAverage
}

func (kind rollingAggregateKind) keepsExtreme(comparison int) bool {
	return kind == rollingMinimum && comparison < 0 || kind == rollingMaximum && comparison > 0
}

func (p *relationalSelectPlan) windowAggregatePartitionValues(rows []relationalResultRow, partition []int, function relationalWindowFunction) ([]exprValue, error) {
	frames, err := p.windowFrameRanges(rows, partition, function.spec)
	if err != nil {
		return nil, err
	}
	inputs, err := p.windowAggregateInputs(rows, partition, function.relationalAggregate)
	if err != nil {
		return nil, err
	}
	state := rollingWindowAggregate{aggregate: function.relationalAggregate, kind: rollingAggregateKindFor(function.name), inputs: inputs}
	if function.distinct {
		state.frequencies = make(map[string]int)
	}
	values := make([]exprValue, len(partition))
	activeStart, activeEnd := 0, -1
	for position, frame := range frames {
		err = state.advance(frame, &activeStart, &activeEnd)
		if err == nil {
			values[position], err = state.result()
		}
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (state *rollingWindowAggregate) advance(frame windowFrameRange, activeStart, activeEnd *int) error {
	if frame.start < *activeStart || frame.end < *activeEnd {
		return sqlFailure{1105, "HY000", "window frame bounds moved backwards"}
	}
	// Remove rows before adding their replacements. This keeps the rolling
	// total inside the value range when two individually valid values would
	// overflow if they were present together for one intermediate step.
	for *activeStart < frame.start {
		if *activeStart <= *activeEnd {
			if err := state.remove(*activeStart); err != nil {
				return err
			}
		}
		*activeStart++
	}
	for *activeEnd < frame.end {
		*activeEnd++
		if err := state.add(*activeEnd); err != nil {
			return err
		}
	}
	return nil
}

func (p *relationalSelectPlan) windowAggregateInputs(rows []relationalResultRow, partition []int, aggregate relationalAggregate) ([]windowAggregateInput, error) {
	inputs := make([]windowAggregateInput, len(partition))
	for position, rowIndex := range partition {
		if aggregate.argument == "*" {
			inputs[position] = windowAggregateInput{value: intValue(1), present: true}
			continue
		}
		value, err := p.windowExpressionValue(rows[rowIndex], aggregate.argument)
		if err != nil {
			return nil, err
		}
		if value.isNull() {
			continue
		}
		inputs[position] = windowAggregateInput{value: value, present: true}
		if aggregate.distinct {
			inputs[position].key = relationalValueKey(aggregate.argument, value, p.source.columns)
		}
	}
	return inputs, nil
}

func (state *rollingWindowAggregate) add(position int) error {
	input := state.inputs[position]
	if !input.present {
		return nil
	}
	if state.skipDistinctAddition(input) {
		return nil
	}
	if state.kind.usesExtremes() {
		if err := state.addExtreme(position); err != nil {
			return err
		}
		state.count++
		return nil
	}
	if state.kind.tracksTotal() {
		if state.count == 0 {
			state.total = aggregateTotalStart(state.aggregate.name, input.value)
		} else {
			var err error
			state.total, err = arithmetic("+", state.total, input.value)
			if err != nil {
				return err
			}
		}
	}
	state.count++
	return nil
}

func (state *rollingWindowAggregate) skipDistinctAddition(input windowAggregateInput) bool {
	if !state.aggregate.distinct || state.kind.usesExtremes() {
		return false
	}
	state.frequencies[input.key]++
	return state.frequencies[input.key] > 1
}

func (state *rollingWindowAggregate) remove(position int) error {
	input := state.inputs[position]
	if !input.present {
		return nil
	}
	if state.skipDistinctRemoval(input) {
		return nil
	}
	if state.kind.usesExtremes() {
		if state.extremeHead < len(state.extremes) && state.extremes[state.extremeHead] == position {
			state.extremeHead++
		}
		state.count--
		return nil
	}
	state.count--
	if !state.kind.tracksTotal() || state.count == 0 {
		return nil
	}
	var err error
	state.total, err = arithmetic("-", state.total, input.value)
	return err
}

func (state *rollingWindowAggregate) skipDistinctRemoval(input windowAggregateInput) bool {
	if !state.aggregate.distinct || state.kind.usesExtremes() {
		return false
	}
	state.frequencies[input.key]--
	if state.frequencies[input.key] > 0 {
		return true
	}
	delete(state.frequencies, input.key)
	return false
}

func (state *rollingWindowAggregate) addExtreme(position int) error {
	for state.hasExtremeCandidate() {
		lastIndex := len(state.extremes) - 1
		last := state.extremes[lastIndex]
		comparison, err := compareOperands(state.inputs[last].value, state.inputs[position].value)
		if err != nil {
			return err
		}
		if state.kind.keepsExtreme(comparison) {
			break
		}
		state.extremes = state.extremes[:lastIndex]
	}
	state.extremes = append(state.extremes, position)
	return nil
}

func (state *rollingWindowAggregate) hasExtremeCandidate() bool {
	return len(state.extremes) > state.extremeHead
}

func (state *rollingWindowAggregate) result() (exprValue, error) {
	switch state.kind {
	case rollingCount:
		return intValue(int64(state.count)), nil
	case rollingMinimum, rollingMaximum:
		if state.count == 0 {
			return nullValue(), nil
		}
		return state.inputs[state.extremes[state.extremeHead]].value, nil
	case rollingAverage:
		if state.count == 0 {
			return nullValue(), nil
		}
		return divideArithmetic(state.total, intValue(int64(state.count)))
	default:
		if state.count == 0 {
			return nullValue(), nil
		}
		return state.total, nil
	}
}
