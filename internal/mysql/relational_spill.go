package mysql

import (
	"container/heap"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// relationalSpillSorter is the external-sort path for ordinary ordered reads.
// It keeps each in-memory run within the statement memory budget, writes sorted
// runs to the operating-system temporary directory, and merges them only while
// result delivery consumes the stream.
type relationalSpillSorter struct {
	plan      *relationalSelectPlan
	rows      []relationalResultRow
	memory    int64
	runs      []relationalSpillRun
	released  map[string]struct{}
	nextRun   int
	inputRows int
	delivery  relationalSpillDelivery
}

type relationalSpillRun struct {
	path  string
	bytes int64
	index int
}

type relationalSpillDelivery struct {
	plan    *relationalSelectPlan
	skipped int
	emitted int
}

type storedRelationalRow struct {
	Values         []string
	Nulls          []bool
	SourceValues   []string
	SourceLockKeys []string
}

type relationalSpillRunWriter struct {
	sorter  *relationalSpillSorter
	file    *os.File
	run     relationalSpillRun
	encoder *gob.Encoder
	output  *temporaryReservationWriter
}

// relationalSpillMerger owns the bounded reader set used to compact external
// sort runs. Keeping it separate makes the sorter responsible for run creation
// and delivery, while this type owns the fixed fan-in merge lifecycle.
type relationalSpillMerger struct {
	sorter *relationalSpillSorter
}

type temporaryReservationWriter struct {
	output    io.Writer
	resources *statementResources
	bytes     int64
}

func (p *relationalSelectPlan) supportsSpillSort() bool {
	if !p.spillSortNeedsBoundedStorage() {
		return false
	}
	return p.spillSortOrdersSupported()
}

func (p *relationalSelectPlan) spillSortNeedsBoundedStorage() bool {
	return p.session != nil && p.session.resources != nil && !p.distinct && !p.hasAggregateOrWindow() && p.source.locking == nil && len(p.order) > 0
}

func (p *relationalSelectPlan) spillSortOrdersSupported() bool {
	for _, order := range p.order {
		if order.computed || order.column < 0 {
			return false
		}
	}
	return true
}

func (p *relationalSelectPlan) spilledSortResult() (*queryResult, error) {
	started := time.Now()
	sorter := relationalSpillSorter{plan: p, delivery: relationalSpillDelivery{plan: p}}
	sourceRows, matchedRows := 0, 0
	err := p.forEachSourceRow(func(row relationRow) error {
		sourceRows++
		matched, err := relationalRowMatches(p.where, row)
		if err != nil || !matched {
			return err
		}
		matchedRows++
		result, err := p.projectRow(row)
		if err != nil {
			return err
		}
		return sorter.append(result)
	})
	if err != nil {
		sorter.close()
		return nil, err
	}
	if err := sorter.finish(); err != nil {
		sorter.close()
		return nil, err
	}
	p.recordReadPipelineCounts(sourceRows, matchedRows, sorter.inputRows, 0, time.Since(started))
	p.recordSpillSort(sorter.inputRows, time.Since(started))
	columns, metadata := p.resultColumns()
	return &queryResult{columns: columns, metadata: metadata, stream: sorter.stream}, nil
}

func (p *relationalSelectPlan) recordSpillSort(rows int, elapsed time.Duration) {
	if p.runtime != nil {
		p.runtime.record(firstOperatorID(p.runtime.sorts), rows, rows, 0, 0, 0, 0, elapsed)
	}
}

func (s *relationalSpillSorter) append(row relationalResultRow) error {
	bytes := int64(spilledRelationalRowMemory(row))
	reserved, err := tryReserveStatementMemory(s.plan.session.resources, bytes)
	if err != nil {
		return err
	}
	if !reserved && len(s.rows) > 0 {
		if err := s.flush(); err != nil {
			return err
		}
		reserved, err = tryReserveStatementMemory(s.plan.session.resources, bytes)
		if err != nil {
			return err
		}
	}
	if reserved {
		s.rows = append(s.rows, row)
		s.memory += bytes
		s.inputRows++
		return nil
	}
	if err := s.writeRun([]relationalResultRow{row}); err != nil {
		return err
	}
	s.inputRows++
	return nil
}

func (s *relationalSpillSorter) finish() error {
	if len(s.runs) > 0 {
		return s.flush()
	}
	sortRelationalRows(s.rows, s.plan.order, s.plan.source.columns)
	return nil
}

func (s *relationalSpillSorter) flush() error {
	if len(s.rows) == 0 {
		return nil
	}
	sortRelationalRows(s.rows, s.plan.order, s.plan.source.columns)
	if err := s.writeRun(s.rows); err != nil {
		return err
	}
	releaseStatementMemory(s.plan.session.resources, s.memory)
	s.rows, s.memory = nil, 0
	return nil
}

func (s *relationalSpillSorter) writeRun(rows []relationalResultRow) error {
	writer, err := s.newRunWriter()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := encodeRelationalSpillRow(writer.encoder, row); err != nil {
			writer.abort()
			return err
		}
	}
	run, err := writer.close()
	if err != nil {
		writer.abort()
		return err
	}
	s.runs = append(s.runs, run)
	s.recordSpill(run)
	return nil
}

func (s *relationalSpillSorter) newRunWriter() (*relationalSpillRunWriter, error) {
	file, err := os.CreateTemp("", "database-sort-*")
	if err != nil {
		return nil, fmt.Errorf("create sort spill: %w", err)
	}
	output := &temporaryReservationWriter{output: file, resources: s.plan.session.resources}
	return &relationalSpillRunWriter{
		sorter: s, file: file, run: relationalSpillRun{path: file.Name(), index: s.nextRun},
		encoder: gob.NewEncoder(output), output: output,
	}, nil
}

func encodeRelationalSpillRow(encoder *gob.Encoder, row relationalResultRow) error {
	// Preserve SQL failures (notably resource exhaustion) so callers return the
	// MySQL error code rather than hiding it behind I/O context.
	return encoder.Encode(storeRelationalRow(row))
}

func (w *relationalSpillRunWriter) close() (relationalSpillRun, error) {
	if err := w.file.Close(); err != nil {
		return relationalSpillRun{}, err
	}
	w.run.bytes = w.output.bytes
	w.sorter.nextRun++
	recordStatementSpill(w.sorter.plan.session.resources, w.run.bytes)
	return w.run, nil
}

func (w *relationalSpillRunWriter) abort() {
	if w == nil {
		return
	}
	_ = w.file.Close()
	w.run.bytes = w.output.bytes
	w.sorter.releaseRun(w.run)
}

func (w *temporaryReservationWriter) Write(bytes []byte) (int, error) {
	if err := reserveStatementTemporary(w.resources, int64(len(bytes))); err != nil {
		return 0, err
	}
	count, err := w.output.Write(bytes)
	if count < len(bytes) {
		releaseStatementTemporary(w.resources, int64(len(bytes)-count))
		if err == nil {
			err = io.ErrShortWrite
		}
	}
	w.bytes += int64(count)
	return count, err
}

func (s *relationalSpillSorter) recordSpill(run relationalSpillRun) {
	if s.plan.runtime != nil {
		s.plan.runtime.metrics.RecordSpill(firstOperatorID(s.plan.runtime.sorts), 1, int(run.bytes), int(run.bytes))
	}
}

func (s *relationalSpillSorter) stream(yield func([]string, []bool) error) error {
	defer s.close()
	if len(s.runs) == 0 {
		return s.delivery.streamRows(s.rows, yield, false)
	}
	return s.streamRuns(yield)
}

func (d *relationalSpillDelivery) streamRows(rows []relationalResultRow, yield func([]string, []bool) error, reserveDelivery bool) error {
	for _, row := range rows {
		if err := d.yield(row, yield, reserveDelivery); errors.Is(err, errStopRelationStream) {
			return nil
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *relationalSpillSorter) streamRuns(yield func([]string, []bool) error) error {
	merger := relationalSpillMerger{sorter: s}
	if err := merger.coalesceRuns(); err != nil {
		return err
	}
	readers, err := merger.runReaders(s.runs)
	if err != nil {
		return err
	}
	defer closeSpillReaders(readers)
	queue := relationalSpillHeap{sorter: s, readers: readers}
	heap.Init(&queue)
	for queue.Len() > 0 {
		reader := heap.Pop(&queue).(*relationalSpillReader)
		if err := s.delivery.yield(reader.row, yield, true); errors.Is(err, errStopRelationStream) {
			return nil
		} else if err != nil {
			return err
		}
		if err := reader.advance(); err != nil {
			return err
		}
		if reader.present {
			heap.Push(&queue, reader)
		}
	}
	return nil
}

func (d *relationalSpillDelivery) yield(row relationalResultRow, yield func([]string, []bool) error, reserveDelivery bool) error {
	if err := d.plan.session.checkStatementResources(); err != nil {
		return err
	}
	if d.shouldSkip() {
		return nil
	}
	values, nulls := d.plan.renderResultRow(row)
	if reserveDelivery {
		release, err := d.plan.session.reserveDeliveredRow(queryResultMemory([][]string{values}, [][]bool{nulls}))
		if err != nil {
			return err
		}
		defer release()
	}
	if err := yield(values, nulls); err != nil {
		return err
	}
	d.emitted++
	if d.plan.limit.present && d.emitted >= d.plan.limit.count {
		return errStopRelationStream
	}
	return nil
}

func (d *relationalSpillDelivery) shouldSkip() bool {
	if !d.plan.limit.present || d.skipped >= d.plan.limit.offset {
		return false
	}
	d.skipped++
	return true
}

func (m relationalSpillMerger) coalesceRuns() error {
	const mergeFanIn = 2
	s := m.sorter
	runCount := len(s.runs)
	for runCount > mergeFanIn {
		next := make([]relationalSpillRun, 0, (runCount+mergeFanIn-1)/mergeFanIn)
		for start := 0; start < runCount; start += mergeFanIn {
			end := min(start+mergeFanIn, runCount)
			if end-start == 1 {
				next = append(next, s.runs[start])
				continue
			}
			run, err := m.mergeRunGroup(s.runs[start:end])
			if err != nil {
				return err
			}
			next = append(next, run)
		}
		s.runs = next
		runCount = len(s.runs)
	}
	return nil
}

func (m relationalSpillMerger) mergeRunGroup(runs []relationalSpillRun) (relationalSpillRun, error) {
	s := m.sorter
	readers, err := m.runReaders(runs)
	if err != nil {
		return relationalSpillRun{}, err
	}
	defer closeSpillReaders(readers)
	writer, err := s.newRunWriter()
	if err != nil {
		return relationalSpillRun{}, err
	}
	queue := relationalSpillHeap{sorter: s, readers: readers}
	heap.Init(&queue)
	for queue.Len() > 0 {
		reader := heap.Pop(&queue).(*relationalSpillReader)
		if err := encodeRelationalSpillRow(writer.encoder, reader.row); err != nil {
			writer.abort()
			return relationalSpillRun{}, err
		}
		if err := reader.advance(); err != nil {
			writer.abort()
			return relationalSpillRun{}, err
		}
		if reader.present {
			heap.Push(&queue, reader)
		}
	}
	run, err := writer.close()
	if err != nil {
		writer.abort()
		return relationalSpillRun{}, err
	}
	run.index = runs[0].index
	closeSpillReaders(readers)
	readers = nil
	for _, source := range runs {
		s.releaseRun(source)
	}
	s.recordSpill(run)
	return run, nil
}

func (m relationalSpillMerger) runReaders(runs []relationalSpillRun) ([]*relationalSpillReader, error) {
	readers := make([]*relationalSpillReader, 0, len(runs))
	for _, run := range runs {
		reader, err := newRelationalSpillReader(m.sorter.plan.session.resources, run)
		if err != nil {
			closeSpillReaders(readers)
			return nil, err
		}
		if reader.present {
			readers = append(readers, reader)
		} else {
			_ = reader.close()
		}
	}
	return readers, nil
}

func (s *relationalSpillSorter) close() {
	releaseStatementMemory(s.plan.session.resources, s.memory)
	s.memory, s.rows = 0, nil
	for _, run := range s.runs {
		s.releaseRun(run)
	}
	s.runs = nil
}

func (s *relationalSpillSorter) releaseRun(run relationalSpillRun) {
	if run.path == "" {
		return
	}
	if s.released == nil {
		s.released = make(map[string]struct{})
	}
	if _, found := s.released[run.path]; found {
		return
	}
	s.released[run.path] = struct{}{}
	_ = os.Remove(run.path)
	releaseStatementTemporary(s.plan.session.resources, run.bytes)
}

func (s *relationalSpillSorter) less(left, right relationalResultRow) bool {
	for index, order := range s.plan.order {
		comparison := compareRelationalSpillOrder(left, right, index, order, s.plan.source.columns)
		if comparison != 0 {
			return orderedBefore(comparison, order.direction)
		}
	}
	return false
}

func compareRelationalSpillOrder(left, right relationalResultRow, orderIndex int, order relationalOrder, columns []relationColumn) int {
	if !order.fromProjection {
		return compareRelationalOrder(left, right, orderIndex, order, columns)
	}
	leftValue, _ := relationColumnValue(columns, order.column, left.source)
	rightValue, _ := relationColumnValue(columns, order.column, right.source)
	if leftValue.isNull() || rightValue.isNull() {
		if leftValue.isNull() && rightValue.isNull() {
			return 0
		}
		if leftValue.isNull() {
			return -1
		}
		return 1
	}
	comparison, err := compareRelationalOrderValues(leftValue, rightValue, &columns[order.column])
	if err == nil {
		return comparison
	}
	return strings.Compare(leftValue.render(), rightValue.render())
}

func spilledRelationalRowMemory(row relationalResultRow) int {
	return relationalResultRowMemory(row) + relationRowMemory(row.source)
}

func storeRelationalRow(row relationalResultRow) storedRelationalRow {
	return storedRelationalRow{
		Values: row.values, Nulls: row.nulls, SourceValues: row.source.values, SourceLockKeys: row.source.lockKeys,
	}
}

func loadRelationalRow(row storedRelationalRow) relationalResultRow {
	return relationalResultRow{
		values: row.Values, nulls: row.Nulls,
		source: relationRow{values: row.SourceValues, lockKeys: row.SourceLockKeys},
	}
}

type relationalSpillReader struct {
	run       relationalSpillRun
	input     relationalSpillInput
	row       relationalResultRow
	resources *statementResources
	memory    int64
	present   bool
}

type relationalSpillInput struct {
	file    *os.File
	decoder *gob.Decoder
}

func newRelationalSpillReader(resources *statementResources, run relationalSpillRun) (*relationalSpillReader, error) {
	file, err := os.Open(run.path)
	if err != nil {
		return nil, fmt.Errorf("open sort spill: %w", err)
	}
	reader := &relationalSpillReader{run: run, input: relationalSpillInput{file: file, decoder: gob.NewDecoder(file)}, resources: resources}
	if err := reader.advance(); err != nil {
		_ = reader.close()
		return nil, err
	}
	return reader, nil
}

func (r *relationalSpillReader) advance() error {
	releaseStatementMemory(r.resources, r.memory)
	r.memory = 0
	var stored storedRelationalRow
	err := r.input.decoder.Decode(&stored)
	if errors.Is(err, io.EOF) {
		r.present = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("read sort spill: %w", err)
	}
	row := loadRelationalRow(stored)
	memory := int64(spilledRelationalRowMemory(row))
	if err := reserveStatementMemory(r.resources, memory); err != nil {
		return err
	}
	r.row, r.memory, r.present = row, memory, true
	return nil
}

func (r *relationalSpillReader) close() error {
	if r == nil {
		return nil
	}
	releaseStatementMemory(r.resources, r.memory)
	r.memory = 0
	return r.input.close()
}

func (i *relationalSpillInput) close() error {
	if i == nil || i.file == nil {
		return nil
	}
	err := i.file.Close()
	i.file = nil
	return err
}

func closeSpillReaders(readers []*relationalSpillReader) {
	for _, reader := range readers {
		_ = reader.close()
	}
}

type relationalSpillHeap struct {
	sorter  *relationalSpillSorter
	readers []*relationalSpillReader
}

func (h relationalSpillHeap) Len() int { return len(h.readers) }

func (h relationalSpillHeap) Less(left, right int) bool {
	leftReader, rightReader := h.readers[left], h.readers[right]
	if h.sorter.less(leftReader.row, rightReader.row) {
		return true
	}
	if h.sorter.less(rightReader.row, leftReader.row) {
		return false
	}
	return leftReader.run.index < rightReader.run.index
}

func (h relationalSpillHeap) Swap(left, right int) {
	h.readers[left], h.readers[right] = h.readers[right], h.readers[left]
}

func (h *relationalSpillHeap) Push(value any) {
	h.readers = append(h.readers, value.(*relationalSpillReader))
}

func (h *relationalSpillHeap) Pop() any {
	last := len(h.readers) - 1
	reader := h.readers[last]
	h.readers = h.readers[:last]
	return reader
}
