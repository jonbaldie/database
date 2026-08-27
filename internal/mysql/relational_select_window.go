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
	for position := 1; position < len(partition); position++ {
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
		if frame.empty() {
			values[position] = nullValue()
			continue
		}
		target, valid, targetErr := positionalWindowTarget(function.name, arguments, frame.end-frame.start+1)
		if targetErr != nil {
			return nil, targetErr
		}
		if !valid {
			values[position] = nullValue()
			continue
		}
		value, valueErr := p.windowExpressionValue(rows[partition[frame.start+target]], arguments[0])
		if valueErr != nil {
			return nil, valueErr
		}
		values[position] = value
	}
	return values, nil
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
	for groupStart := 0; groupStart < len(partition); {
		groupEnd := groupStart
		for groupEnd+1 < len(partition) {
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
		if current.isNull() {
			ranges[position] = windowFrameRange{start: peerStarts[position], end: peerEnds[position]}
			continue
		}
		lower, lowerOpen, err := rangeFrameBoundary(current, frame.start, order.direction, true)
		if err != nil {
			return nil, err
		}
		upper, upperOpen, err := rangeFrameBoundary(current, frame.end, order.direction, false)
		if err != nil {
			return nil, err
		}
		start, end := nonNullStart, nonNullEnd
		if !lowerOpen {
			start, err = windowRangeLowerBound(orderValues, nonNullStart, nonNullEnd+1, lower, order.direction)
			if err != nil {
				return nil, err
			}
		}
		if !upperOpen {
			upperEnd, upperErr := windowRangeUpperBound(orderValues, nonNullStart, nonNullEnd+1, upper, order.direction)
			if upperErr != nil {
				return nil, upperErr
			}
			end = upperEnd - 1
		}
		ranges[position] = windowFrameRange{start: start, end: end}
	}
	return ranges, nil
}

func windowValuePeerBounds(values []exprValue, order relationalWindowOrder, plan *relationalSelectPlan) ([]int, []int) {
	starts := make([]int, len(values))
	ends := make([]int, len(values))
	for groupStart := 0; groupStart < len(values); {
		groupEnd := groupStart
		for groupEnd+1 < len(values) && plan.compareWindowOrderValues(order, values[groupEnd], values[groupEnd+1]) == 0 {
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
	for start < len(values) && values[start].isNull() {
		start++
	}
	end := len(values) - 1
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
	inputs      []windowAggregateInput
	frequencies map[string]int
	count       int
	total       exprValue
	extremes    []int
	extremeHead int
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
	state := rollingWindowAggregate{aggregate: function.relationalAggregate, inputs: inputs}
	if function.distinct {
		state.frequencies = make(map[string]int)
	}
	values := make([]exprValue, len(partition))
	activeStart, activeEnd := 0, -1
	for position, frame := range frames {
		if frame.start < activeStart || frame.end < activeEnd {
			return nil, sqlFailure{1105, "HY000", "window frame bounds moved backwards"}
		}
		for activeEnd < frame.end {
			activeEnd++
			if err := state.add(activeEnd); err != nil {
				return nil, err
			}
		}
		for activeStart < frame.start {
			if activeStart <= activeEnd {
				if err := state.remove(activeStart); err != nil {
					return nil, err
				}
			}
			activeStart++
		}
		values[position], err = state.result()
		if err != nil {
			return nil, err
		}
	}
	return values, nil
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
	if state.aggregate.distinct && state.aggregate.name != "MIN" && state.aggregate.name != "MAX" {
		state.frequencies[input.key]++
		if state.frequencies[input.key] > 1 {
			return nil
		}
	}
	if state.aggregate.name == "MIN" || state.aggregate.name == "MAX" {
		if err := state.addExtreme(position); err != nil {
			return err
		}
		state.count++
		return nil
	}
	if state.aggregate.name != "COUNT" {
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

func (state *rollingWindowAggregate) remove(position int) error {
	input := state.inputs[position]
	if !input.present {
		return nil
	}
	if state.aggregate.distinct && state.aggregate.name != "MIN" && state.aggregate.name != "MAX" {
		state.frequencies[input.key]--
		if state.frequencies[input.key] > 0 {
			return nil
		}
		delete(state.frequencies, input.key)
	}
	if state.aggregate.name == "MIN" || state.aggregate.name == "MAX" {
		if state.extremeHead < len(state.extremes) && state.extremes[state.extremeHead] == position {
			state.extremeHead++
		}
		state.count--
		return nil
	}
	state.count--
	if state.aggregate.name == "COUNT" || state.count == 0 {
		return nil
	}
	var err error
	state.total, err = arithmetic("-", state.total, input.value)
	return err
}

func (state *rollingWindowAggregate) addExtreme(position int) error {
	for len(state.extremes) > state.extremeHead {
		last := state.extremes[len(state.extremes)-1]
		comparison, err := compareOperands(state.inputs[last].value, state.inputs[position].value)
		if err != nil {
			return err
		}
		if (state.aggregate.name == "MIN" && comparison < 0) || (state.aggregate.name == "MAX" && comparison > 0) {
			break
		}
		state.extremes = state.extremes[:len(state.extremes)-1]
	}
	state.extremes = append(state.extremes, position)
	return nil
}

func (state *rollingWindowAggregate) result() (exprValue, error) {
	if state.aggregate.name == "COUNT" {
		return intValue(int64(state.count)), nil
	}
	if state.count == 0 {
		return nullValue(), nil
	}
	if state.aggregate.name == "MIN" || state.aggregate.name == "MAX" {
		return state.inputs[state.extremes[state.extremeHead]].value, nil
	}
	if state.aggregate.name == "AVG" {
		return divideArithmetic(state.total, intValue(int64(state.count)))
	}
	return state.total, nil
}
