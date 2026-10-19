package v2service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/core/block/editor/state"
	"github.com/anyproto/anytype-heart/core/block/editor/table"
)

// tableOpsDoc is a 3x3 table whose every row carries one cell per column, so
// a row or column op that drops or misplaces a cell cannot hide behind the
// normalization a ragged fixture would get.
const tableOpsDoc = `{"formatVersion":"2.0","id":"obj1","type":"page","blocks":[` +
	`{"id":"tblOne1","type":"table",` +
	`"columns":[{"id":"colA"},{"id":"colB"},{"id":"colC"}],` +
	`"rows":[{"id":"rowH","is_header":true,"cells":["Name","Status","Owner"]},` +
	`{"id":"rowB","cells":["Export","Done","Ann"]},` +
	`{"id":"rowC","cells":["Import","Todo","Bob"]}]}]}`

// tableNoRowsDoc is a table that has columns but no rows yet, and
// tableNoColumnsDoc the mirror image: one row with nothing to put in it. Both
// exercise an op whose axis has no part to anchor on.
const tableNoRowsDoc = `{"formatVersion":"2.0","id":"obj1","type":"page","blocks":[` +
	`{"id":"tblOne1","type":"table","columns":[{"id":"colA"},{"id":"colB"}],"rows":[]}]}`

const tableNoColumnsDoc = `{"formatVersion":"2.0","id":"obj1","type":"page","blocks":[` +
	`{"id":"tblOne1","type":"table","columns":[],"rows":[{"id":"rowA","cells":[]}]}]}`

// blockIndents reads the rendered indent of every block (absent reads as 0).
func blockIndents(blocks []map[string]any) []int {
	out := make([]int, len(blocks))
	for i, b := range blocks {
		f, _ := b["indent"].(float64)
		out[i] = int(f)
	}
	return out
}

// renderedTable returns the one table block of a captured state.
func renderedTable(t *testing.T, st *state.State) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, b := range docBlocks(stateDoc(t, st)) {
		if b["type"] == "table" {
			found = append(found, b)
		}
	}
	require.Len(t, found, 1, "expected exactly one table in the document")
	return found[0]
}

// tableRows is the rendered rows array.
func tableRows(t *testing.T, table map[string]any) []map[string]any {
	t.Helper()
	raw, ok := table["rows"].([]any)
	require.True(t, ok, "table carries no rows: %v", table)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

// rowCells is one rendered row's cells.
func rowCells(t *testing.T, row map[string]any) []any {
	t.Helper()
	raw, ok := row["cells"].([]any)
	require.True(t, ok, "row carries no cells: %v", row)
	return raw
}

// allRowCells is every rendered row's cells, so a per-row cell shift shows up
// whole.
func allRowCells(t *testing.T, table map[string]any) [][]any {
	t.Helper()
	rows := tableRows(t, table)
	out := make([][]any, len(rows))
	for i, r := range rows {
		out[i] = rowCells(t, r)
	}
	return out
}

// stateRows reads the table's row ids and their cell texts straight from the
// captured state, which is where "one cell per column" holds: the document
// render omits TRAILING empty cells (A6.1), so a row that was filled but is
// still blank renders with no cells at all.
func stateRows(t *testing.T, st *state.State, tableId string) (ids []string, cells [][]string) {
	t.Helper()
	tb, err := table.NewTable(st, tableId)
	require.NoError(t, err)
	for _, rowID := range tb.RowIDs() {
		row := st.Get(rowID)
		require.NotNil(t, row, "row %s", rowID)
		ids = append(ids, rowID)
		texts := make([]string, 0, len(row.Model().ChildrenIds))
		for _, cellID := range row.Model().ChildrenIds {
			cell := st.Get(cellID)
			require.NotNil(t, cell, "cell %s of row %s", cellID, rowID)
			texts = append(texts, cell.Model().GetText().GetText())
		}
		cells = append(cells, texts)
	}
	return ids, cells
}

func TestPatchObjectDuplicateBlock(t *testing.T) {
	ctx := context.Background()

	t.Run("copies a subtree with fresh ids next to the source", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, editBaseDoc), "headB")

		result, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_block","id":"blockParent1","after":"blockParent1"}`), "", false, true)

		require.NoError(t, err)
		assert.Equal(t, v2model.DiffStats{BlocksAdded: 2}, result.DiffStats)
		blocks := docBlocks(stateDoc(t, *captured))
		assert.Equal(t, []string{"Section", "parent", "child", "parent", "child", "the Q3 report and Q3 plan"}, blockTexts(blocks))
		assert.Equal(t, []int{0, 0, 1, 0, 1, 0}, blockIndents(blocks))
		// the source keeps its own identity; the copy is a new block
		assert.Equal(t, "blockParent1", blocks[1]["id"])
		assert.NotEqual(t, "blockParent1", blocks[3]["id"])
		assert.NotEqual(t, "blockChild1", blocks[4]["id"])
	})

	t.Run("lands inside the source itself", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, editBaseDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_block","id":"blockChild1","inside":"blockParent1"}`), "", false, true)

		require.NoError(t, err)
		blocks := docBlocks(stateDoc(t, *captured))
		assert.Equal(t, []int{0, 0, 1, 1, 0}, blockIndents(blocks))
		assert.Equal(t, []string{"Section", "parent", "child", "child", "the Q3 report and Q3 plan"}, blockTexts(blocks))
	})

	t.Run("no targeting puts the copy at the end of the document", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, editBaseDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_block","id":"blockHeading1"}`), "", false, true)

		require.NoError(t, err)
		blocks := docBlocks(stateDoc(t, *captured))
		assert.Equal(t, "Section", blocks[len(blocks)-1]["text"])
	})

	t.Run("copies a table block, wrappers and all", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, editTableDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_block","id":"tblOne1","after":"tblOne1"}`), "", false, true)

		require.NoError(t, err)
		var tables []map[string]any
		for _, b := range docBlocks(stateDoc(t, *captured)) {
			if b["type"] == "table" {
				tables = append(tables, b)
			}
		}
		require.Len(t, tables, 2)
		// the copy is a new table: its own block id, and its own column
		// wrappers (the row and column layout ids are re-minted)
		assert.NotEqual(t, tables[0]["id"], tables[1]["id"])
		firstCols, _ := tables[0]["columns"].([]any)
		copyCols, _ := tables[1]["columns"].([]any)
		assert.Len(t, copyCols, len(firstCols))
		assert.NotEqual(t, firstCols[0].(map[string]any)["id"], copyCols[0].(map[string]any)["id"])
		assert.Equal(t, allRowCells(t, tables[0]), allRowCells(t, tables[1]))
	})

	t.Run("an unknown block is refused", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, editBaseDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_block","id":"nope"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "not_found", apiErr.Code)
		assert.Equal(t, 404, apiErr.Status)
	})
}

func TestPatchObjectTableRowOps(t *testing.T) {
	ctx := context.Background()

	t.Run("insert_table_row appends a row and fills its cells", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1"}`), "", false, true)

		require.NoError(t, err)
		ids, cells := stateRows(t, *captured, "tblOne1")
		require.Len(t, ids, 4)
		assert.Equal(t, [][]string{
			{"Name", "Status", "Owner"},
			{"Export", "Done", "Ann"},
			{"Import", "Todo", "Bob"},
			{"", "", ""},
		}, cells)
		// a blank row renders with no cells at all (trailing empties omitted)
		rows := tableRows(t, renderedTable(t, *captured))
		require.Len(t, rows, 4)
		_, hasCells := rows[3]["cells"]
		assert.False(t, hasCells)
	})

	t.Run("insert_table_row gives a table with no rows its first one", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableNoRowsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1"}`), "", false, true)

		require.NoError(t, err)
		ids, cells := stateRows(t, *captured, "tblOne1")
		require.Len(t, ids, 1)
		assert.Equal(t, [][]string{{"", ""}}, cells)
	})

	t.Run("insert_table_row lands before a named row", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1","before":"rowC"}`), "", false, true)

		require.NoError(t, err)
		ids, cells := stateRows(t, *captured, "tblOne1")
		require.Len(t, ids, 4)
		assert.NotEqual(t, "rowC", ids[2])
		assert.Equal(t, "rowC", ids[3])
		assert.Equal(t, []string{"", "", ""}, cells[2])
		assert.Equal(t, []string{"Import", "Todo", "Bob"}, cells[3])
	})

	t.Run("insert_table_row after a row named by its first cell", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1","after":"Export"}`), "", false, true)

		require.NoError(t, err)
		ids, cells := stateRows(t, *captured, "tblOne1")
		require.Len(t, ids, 4)
		assert.Equal(t, "rowB", ids[1])
		assert.Equal(t, []string{"", "", ""}, cells[2])
		assert.Equal(t, "rowC", ids[3])
	})

	t.Run("delete_table_row removes the row and its cells", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"delete_table_row","table_id":"tblOne1","row":"rowB"}`), "", false, true)

		require.NoError(t, err)
		table := renderedTable(t, *captured)
		require.Len(t, tableRows(t, table), 2)
		assert.Equal(t, [][]any{{"Name", "Status", "Owner"}, {"Import", "Todo", "Bob"}}, allRowCells(t, table))
	})

	t.Run("duplicate_table_row copies the row right after its source", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_table_row","table_id":"tblOne1","row":"rowB"}`), "", false, true)

		require.NoError(t, err)
		assert.Equal(t, [][]any{
			{"Name", "Status", "Owner"},
			{"Export", "Done", "Ann"},
			{"Export", "Done", "Ann"},
			{"Import", "Todo", "Bob"},
		}, allRowCells(t, renderedTable(t, *captured)))
	})

	t.Run("set_table_row_header promotes the row above the first row", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"set_table_row_header","table_id":"tblOne1","row":"rowB","is_header":true}`), "", false, true)

		require.NoError(t, err)
		table := renderedTable(t, *captured)
		rows := tableRows(t, table)
		// both rows are headers now, and header rows sort to the front: the
		// promoted row leads with its own first cell
		assert.Equal(t, true, rows[0]["is_header"])
		assert.Equal(t, true, rows[1]["is_header"])
		assert.Contains(t, []any{"Export", "Name"}, rowCells(t, rows[0])[0])
		assert.Equal(t, []any{"Import", "Todo", "Bob"}, rowCells(t, rows[2]))
	})

	t.Run("set_table_row_header clears the flag", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"set_table_row_header","table_id":"tblOne1","row":"rowH","is_header":false}`), "", false, true)

		require.NoError(t, err)
		table := renderedTable(t, *captured)
		rows := tableRows(t, table)
		require.Len(t, rows, 3)
		for i, row := range rows {
			_, hasHeader := row["is_header"]
			assert.False(t, hasHeader, "row %d still carries a header flag", i)
		}
	})

	t.Run("an unknown row is refused", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"delete_table_row","table_id":"tblOne1","row":"nope"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "not_found", apiErr.Code)
		assert.Equal(t, 404, apiErr.Status)
	})
}

func TestPatchObjectTableColumnOps(t *testing.T) {
	ctx := context.Background()

	t.Run("insert_table_column appends a column and a cell to every row", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_column","table_id":"tblOne1"}`), "", false, true)

		require.NoError(t, err)
		_, cells := stateRows(t, *captured, "tblOne1")
		assert.Equal(t, [][]string{
			{"Name", "Status", "Owner", ""},
			{"Export", "Done", "Ann", ""},
			{"Import", "Todo", "Bob", ""},
		}, cells)
		columns, _ := renderedTable(t, *captured)["columns"].([]any)
		assert.Len(t, columns, 4)
	})

	t.Run("insert_table_column gives a table with no columns its first one", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableNoColumnsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_column","table_id":"tblOne1"}`), "", false, true)

		require.NoError(t, err)
		_, cells := stateRows(t, *captured, "tblOne1")
		assert.Equal(t, [][]string{{""}}, cells)
	})

	t.Run("insert_table_column lands before a column named by its header", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_column","table_id":"tblOne1","before":"Status"}`), "", false, true)

		require.NoError(t, err)
		_, cells := stateRows(t, *captured, "tblOne1")
		assert.Equal(t, [][]string{
			{"Name", "", "Status", "Owner"},
			{"Export", "", "Done", "Ann"},
			{"Import", "", "Todo", "Bob"},
		}, cells)
	})

	t.Run("delete_table_column removes the column and its cells", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"delete_table_column","table_id":"tblOne1","col":"Status"}`), "", false, true)

		require.NoError(t, err)
		assert.Equal(t, [][]any{
			{"Name", "Owner"},
			{"Export", "Ann"},
			{"Import", "Bob"},
		}, allRowCells(t, renderedTable(t, *captured)))
	})

	t.Run("duplicate_table_column copies the column right after its source", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"duplicate_table_column","table_id":"tblOne1","col":"colB"}`), "", false, true)

		require.NoError(t, err)
		table := renderedTable(t, *captured)
		columns, _ := table["columns"].([]any)
		require.Len(t, columns, 4)
		assert.Equal(t, [][]any{
			{"Name", "Status", "Status", "Owner"},
			{"Export", "Done", "Done", "Ann"},
			{"Import", "Todo", "Todo", "Bob"},
		}, allRowCells(t, table))
	})

	t.Run("move_table_column moves it to a new place", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"move_table_column","table_id":"tblOne1","col":"colC","before":"colA"}`), "", false, true)

		require.NoError(t, err)
		assert.Equal(t, [][]any{
			{"Owner", "Name", "Status"},
			{"Ann", "Export", "Done"},
			{"Bob", "Import", "Todo"},
		}, allRowCells(t, renderedTable(t, *captured)))
	})

	t.Run("moving a column to its own place changes nothing", func(t *testing.T) {
		fx := newV2Fixture(t)
		captured := fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"move_table_column","table_id":"tblOne1","col":"colA","position":"first"}`), "", false, true)

		require.NoError(t, err)
		assert.Equal(t, [][]any{
			{"Name", "Status", "Owner"},
			{"Export", "Done", "Ann"},
			{"Import", "Todo", "Bob"},
		}, allRowCells(t, renderedTable(t, *captured)))
	})

	t.Run("a destination is required", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"move_table_column","table_id":"tblOne1","col":"colA"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "validation_failed", apiErr.Code)
		assert.Equal(t, 400, apiErr.Status)
	})

	t.Run("after and before together are ambiguous", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1","after":"rowB","before":"rowC"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "ambiguous_input", apiErr.Code)
		assert.Equal(t, 400, apiErr.Status)
	})

	t.Run("a position with after is refused", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, tableOpsDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"tblOne1","after":"rowB","position":"last"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "validation_failed", apiErr.Code)
		assert.Equal(t, 400, apiErr.Status)
	})

	t.Run("table_id must name a table", func(t *testing.T) {
		fx := newV2Fixture(t)
		fx.expectMutate(editRead(t, editBaseDoc), "headB")

		_, err := fx.PatchObject(ctx, testSpaceId, "obj1",
			patchBody(`{"op":"insert_table_row","table_id":"blockParent1"}`), "", false, true)

		apiErr := v2Err(t, err)
		assert.Equal(t, "validation_failed", apiErr.Code)
		assert.Equal(t, 400, apiErr.Status)
	})
}
