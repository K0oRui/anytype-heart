package v2service

// tableops.go holds the table row and column ops: the structural edits a
// table needs beyond set_cell. They go through the native table editor
// (core/block/editor/table), which owns the format-internal
// column/row/cell graph (SPEC §6.1), so a duplicated row keeps its cells and
// a moved column keeps its cells in the row order the column index implies.
// Authoring the same change as a whole-table replace_subtree would re-mint
// every wrapper id and lose every row's identity (the review A′3 bug), which
// is exactly what these ops exist to avoid.

import (
	"fmt"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/core/block/editor/table"
	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
)

// v2TableEditor is the native table editor with no SmartBlock behind it: its
// methods act on the state they are handed (the same child state the ops
// edit) and consult the SmartBlock only for the object-level Blocks
// restriction, which the PATCH pipeline has already checked.
var v2TableEditor = table.NewEditor(nil)

// tableAxis names one table axis: the document member holding its parts and
// the word a refusal calls one part by.
type tableAxis struct {
	field string // "rows" | "columns"
	one   string // "row" | "column"
}

var (
	tableRowsAxis    = tableAxis{field: "rows", one: "row"}
	tableColumnsAxis = tableAxis{field: "columns", one: "column"}
)

// tableFor resolves the table block an op addresses and returns both its
// document form (which the part refs resolve against) and the native table
// helper (the state the edits land in).
func (a *v2StateApplier) tableFor(tableRef, opPath string) (map[string]any, *table.Table, error) {
	doc, err := a.doc()
	if err != nil {
		return nil, nil, err
	}
	idx, err := a.resolveRef(doc, tableRef, opPath+".table_id")
	if err != nil {
		return nil, nil, err
	}
	block := doc.blocks[idx]
	if typ := blockType(block); typ != "table" {
		return nil, nil, v2model.ValidationFailed(
			fmt.Sprintf("block %q is a %q block, not a table", tableRef, typ),
			v2model.Issue{Path: opPath + ".table_id", Message: "this op addresses a table block"})
	}
	tb, err := table.NewTable(a.st, blockId(block))
	if err != nil {
		return nil, nil, fmt.Errorf("table %s: %w", tableRef, err)
	}
	return block, tb, nil
}

// tablePartId resolves one row or column ref to its full id, on the same
// id-or-label contract set_cell's row and col slots follow.
func (a *v2StateApplier) tablePartId(tableBlock map[string]any, axis tableAxis, ref, tableRef, opPath, refField string) (string, error) {
	i, err := resolveTablePart(tableBlock, axis.field, ref, tableRef, opPath+"."+refField)
	if err != nil {
		return "", err
	}
	entries, _ := tableBlock[axis.field].([]any)
	if m, ok := entries[i].(map[string]any); ok {
		if id := blockId(m); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("table %s: %s %d carries no id", tableRef, axis.one, i)
}

// axisPartIds reads the axis's part ids in document order.
func axisPartIds(tableBlock map[string]any, axis tableAxis) []string {
	entries, _ := tableBlock[axis.field].([]any)
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok {
			if id := blockId(m); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// tablePartPlacement resolves where a part lands within one axis, the
// table-scoped twin of resolveTarget. after and before name a sibling part by
// ref (id, unique suffix, or the label a read shows); position names one end
// of the axis. At most one of the three is meaningful, and position does not
// travel with after/before, because the slot is already named. Omitting all
// three picks the axis end: last by default, first with position set to it.
//
// empty reports an axis with no parts at all: there is no part to anchor on,
// so an insert resolves through the table block instead.
func (a *v2StateApplier) tablePartPlacement(tableBlock map[string]any, axis tableAxis, after, before, position, tableRef, opPath string) (anchorId string, placeBefore bool, empty bool, err error) {
	switch {
	case after != "" && before != "":
		return "", false, false, v2model.AmbiguousInput(
			"at most one of after, before is allowed",
			v2model.Issue{Path: opPath, Message: "after and before are alternative anchors within the table"})
	case position != "" && (after != "" || before != ""):
		return "", false, false, v2model.ValidationFailed("position does not travel with after or before",
			v2model.Issue{
				Path:    opPath + ".position",
				Message: fmt.Sprintf("position picks one end of the axis; %q already named the slot", after+before)})
	case position != "":
		if err := checkPosition(position, opPath); err != nil {
			return "", false, false, err
		}
	}
	if ref, field := before, "before"; ref != "" || after != "" {
		if ref == "" {
			ref, field = after, "after"
		}
		id, err := a.tablePartId(tableBlock, axis, ref, tableRef, opPath, field)
		if err != nil {
			return "", false, false, err
		}
		return id, field == "before", false, nil
	}
	ids := axisPartIds(tableBlock, axis)
	if len(ids) == 0 {
		return "", false, true, nil
	}
	if position == "first" {
		return ids[0], true, false, nil
	}
	return ids[len(ids)-1], false, false, nil
}

// partPosition maps the before/after axis vocabulary to the state's insert
// positions: Block_Top lands before the anchor, Block_Bottom after it.
func partPosition(before bool) model.BlockPosition {
	if before {
		return model.Block_Top
	}
	return model.Block_Bottom
}

// tablePlaceTarget is the placement resolution the duplicate and move ops
// share: the caller's explicit targeting, or the op's own default when none is
// given (a duplicate follows its source; a move always names a destination and
// never reaches the default).
func (a *v2StateApplier) tablePlaceTarget(tableBlock map[string]any, axis tableAxis, after, before, position string, defId string, defBefore bool, tableRef, opPath string) (string, bool, bool, error) {
	if after == "" && before == "" && position == "" {
		return defId, defBefore, false, nil
	}
	return a.tablePartPlacement(tableBlock, axis, after, before, position, tableRef, opPath)
}

// applyInsertTableRow appends a row (or inserts one after/before a sibling)
// and fills its cells for every column.
func (a *v2StateApplier) applyInsertTableRow(op opInsertTableRow, opPath string) error {
	tableBlock, tb, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	anchorId, before, empty, err := a.tablePartPlacement(tableBlock, tableRowsAxis, op.After, op.Before, op.Position, op.TableId, opPath)
	if err != nil {
		return err
	}
	position := partPosition(before)
	if empty {
		// no row to anchor on: the table block with Block_Inner appends
		// through the rows wrapper (an empty axis has no sibling slot)
		anchorId, position = tb.Block().Model().Id, model.Block_Inner
	}
	newRowId, err := v2TableEditor.RowCreate(a.st, pb.RpcBlockTableRowCreateRequest{
		TargetId: anchorId,
		Position: position,
	})
	if err != nil {
		return fmt.Errorf("insert table row: %w", err)
	}
	if err := v2TableEditor.RowListFill(a.st, pb.RpcBlockTableRowListFillRequest{BlockIds: []string{newRowId}}); err != nil {
		return fmt.Errorf("fill the new table row: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyDeleteTableRow(op opDeleteTableRow, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	rowId, err := a.tablePartId(tableBlock, tableRowsAxis, op.Row, op.TableId, opPath, "row")
	if err != nil {
		return err
	}
	if err := v2TableEditor.RowDelete(a.st, pb.RpcBlockTableRowDeleteRequest{TargetId: rowId}); err != nil {
		return fmt.Errorf("delete table row: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyDuplicateTableRow(op opDuplicateTableRow, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	srcId, err := a.tablePartId(tableBlock, tableRowsAxis, op.Row, op.TableId, opPath, "row")
	if err != nil {
		return err
	}
	// the default is the copy right under its source, which is what
	// "duplicate" means everywhere else
	anchorId, before, _, err := a.tablePlaceTarget(tableBlock, tableRowsAxis, op.After, op.Before, op.Position, srcId, false, op.TableId, opPath)
	if err != nil {
		return err
	}
	if _, err := v2TableEditor.RowDuplicate(a.st, pb.RpcBlockTableRowDuplicateRequest{
		BlockId:  srcId,
		TargetId: anchorId,
		Position: partPosition(before),
	}); err != nil {
		return fmt.Errorf("duplicate table row: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applySetTableRowHeader(op opSetTableRowHeader, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	rowId, err := a.tablePartId(tableBlock, tableRowsAxis, op.Row, op.TableId, opPath, "row")
	if err != nil {
		return err
	}
	if err := v2TableEditor.RowSetHeader(a.st, pb.RpcBlockTableRowSetHeaderRequest{
		TargetId: rowId,
		IsHeader: op.IsHeader,
	}); err != nil {
		return fmt.Errorf("set table row header: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyInsertTableColumn(op opInsertTableColumn, opPath string) error {
	tableBlock, tb, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	anchorId, before, empty, err := a.tablePartPlacement(tableBlock, tableColumnsAxis, op.After, op.Before, op.Position, op.TableId, opPath)
	if err != nil {
		return err
	}
	position := columnPosition(before)
	if empty {
		anchorId, position = tb.Block().Model().Id, model.Block_Inner
	}
	if _, err := v2TableEditor.ColumnCreate(a.st, pb.RpcBlockTableColumnCreateRequest{
		TargetId: anchorId,
		Position: position,
	}); err != nil {
		return fmt.Errorf("insert table column: %w", err)
	}
	// ColumnCreate adds only the column header; RowListFill gives every row a
	// cell for it (and puts each row's cells back in column order), so the
	// stored table keeps one cell per column rather than relying on the
	// render's nil padding
	if rows := tb.RowIDs(); len(rows) > 0 {
		if err := v2TableEditor.RowListFill(a.st, pb.RpcBlockTableRowListFillRequest{BlockIds: rows}); err != nil {
			return fmt.Errorf("fill the table's rows for the new column: %w", err)
		}
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyDeleteTableColumn(op opDeleteTableColumn, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	colId, err := a.tablePartId(tableBlock, tableColumnsAxis, op.Col, op.TableId, opPath, "col")
	if err != nil {
		return err
	}
	if err := v2TableEditor.ColumnDelete(a.st, pb.RpcBlockTableColumnDeleteRequest{TargetId: colId}); err != nil {
		return fmt.Errorf("delete table column: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyDuplicateTableColumn(op opDuplicateTableColumn, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	srcId, err := a.tablePartId(tableBlock, tableColumnsAxis, op.Col, op.TableId, opPath, "col")
	if err != nil {
		return err
	}
	anchorId, before, _, err := a.tablePlaceTarget(tableBlock, tableColumnsAxis, op.After, op.Before, op.Position, srcId, false, op.TableId, opPath)
	if err != nil {
		return err
	}
	if _, err := v2TableEditor.ColumnDuplicate(a.st, pb.RpcBlockTableColumnDuplicateRequest{
		BlockId:  srcId,
		TargetId: anchorId,
		Position: columnPosition(before),
	}); err != nil {
		return fmt.Errorf("duplicate table column: %w", err)
	}
	a.mutated()
	return nil
}

func (a *v2StateApplier) applyMoveTableColumn(op opMoveTableColumn, opPath string) error {
	tableBlock, _, err := a.tableFor(op.TableId, opPath)
	if err != nil {
		return err
	}
	colId, err := a.tablePartId(tableBlock, tableColumnsAxis, op.Col, op.TableId, opPath, "col")
	if err != nil {
		return err
	}
	if op.After == "" && op.Before == "" && op.Position == "" {
		return v2model.ValidationFailed("move_table_column needs a destination",
			v2model.Issue{Path: opPath, Message: "give after, before or position"})
	}
	anchorId, before, _, err := a.tablePartPlacement(tableBlock, tableColumnsAxis, op.After, op.Before, op.Position, op.TableId, opPath)
	if err != nil {
		return err
	}
	if anchorId == colId {
		// already where the caller asked for it: the native move unlinks the
		// column before re-inserting it and would then have no anchor left
		return nil
	}
	if err := v2TableEditor.ColumnMove(a.st, pb.RpcBlockTableColumnMoveRequest{
		TargetId:     colId,
		DropTargetId: anchorId,
		Position:     columnPosition(before),
	}); err != nil {
		return fmt.Errorf("move table column: %w", err)
	}
	a.mutated()
	return nil
}

// columnPosition is partPosition in the column vocabulary the native column
// editor takes: Left lands before the anchor column, Right after it. The
// editor translates both to the state's Top/Bottom before inserting, so a
// column move never goes through the layout row/column wrapper path.
func columnPosition(before bool) model.BlockPosition {
	if before {
		return model.Block_Left
	}
	return model.Block_Right
}
