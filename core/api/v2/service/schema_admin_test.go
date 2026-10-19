package v2service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	v2model "github.com/anyproto/anytype-heart/core/api/v2/model"
	"github.com/anyproto/anytype-heart/core/domain"
	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/localstore/objectstore"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
)

// seedPriorityProperty registers one select property with two options. The
// stored order (opt-low first) is the reverse of the order the tests request,
// so a response that read the order back is distinguishable from one that
// echoed the request.
func seedPriorityProperty(t *testing.T) *v2Fixture {
	t.Helper()
	fx := newV2Fixture(t)
	fx.addRelation(t, testSpaceId, objectstore.TestObject{
		bundle.RelationKeyId:             domain.String("rel-priority"),
		bundle.RelationKeyRelationKey:    domain.String("priority"),
		bundle.RelationKeyApiObjectKey:   domain.String("priority"),
		bundle.RelationKeyName:           domain.String("Priority"),
		bundle.RelationKeyRelationFormat: domain.Int64(int64(model.RelationFormat_status)),
	})
	fx.addOption(t, "opt-high", "High", "red", "b")
	fx.addOption(t, "opt-low", "Low", "", "a")
	return fx
}

// addOption registers one option object of the priority property.
func (fx *v2Fixture) addOption(t *testing.T, id, name, color, order string) {
	t.Helper()
	obj := objectstore.TestObject{
		bundle.RelationKeyId:             domain.String(id),
		bundle.RelationKeyRelationKey:    domain.String("priority"),
		bundle.RelationKeyName:           domain.String(name),
		bundle.RelationKeyOrderId:        domain.String(order),
		bundle.RelationKeyResolvedLayout: domain.Int64(int64(model.ObjectType_relationOption)),
	}
	if color != "" {
		obj[bundle.RelationKeyRelationOptionColor] = domain.String(color)
	}
	fx.objectStore.AddObjects(t, testSpaceId, []objectstore.TestObject{obj})
}

// holdOption registers one object whose priority holds the given option.
func (fx *v2Fixture) holdOption(t *testing.T, objectId, optionId string) {
	t.Helper()
	fx.objectStore.AddObjects(t, testSpaceId, []objectstore.TestObject{{
		bundle.RelationKeyId:             domain.String(objectId),
		bundle.RelationKeyName:           domain.String("Task"),
		bundle.RelationKeyResolvedLayout: domain.Int64(int64(model.ObjectType_basic)),
		domain.RelationKey("priority"):   domain.String(optionId),
	}})
}

func TestV2RemovePropertyOption(t *testing.T) {
	t.Run("an option named by its text is removed by id", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)
		var got *pb.RpcRelationListRemoveOptionRequest
		fx.mwMock.EXPECT().RelationListRemoveOption(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcRelationListRemoveOptionRequest) *pb.RpcRelationListRemoveOptionResponse {
				got = req
				return &pb.RpcRelationListRemoveOptionResponse{}
			})

		// when
		result, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "priority", "High", false, false)

		// then: the caller's spelling never reaches the RPC, only the resolved id
		require.NoError(t, err)
		assert.False(t, result.DryRun)
		require.NotNil(t, got)
		assert.Equal(t, []string{"opt-high"}, got.OptionIds)
		assert.False(t, got.CheckInObjects, "the route ran the usage check itself")
	})

	t.Run("an option objects hold is refused while it is in use", func(t *testing.T) {
		// given: one object holds the option, and no RPC expectation is
		// registered, so reaching the RPC fails the test
		fx := seedPriorityProperty(t)
		fx.holdOption(t, "obj-task", "opt-high")

		// when
		_, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "priority", "High", false, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusConflict, apiErr.Status)
		assert.Equal(t, v2model.CodeConflict, apiErr.Code)
		require.Len(t, apiErr.Issues, 1)
		assert.Contains(t, apiErr.Issues[0].Hint, "delete_in_objects=true")
	})

	t.Run("delete_in_objects removes it anyway", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)
		fx.holdOption(t, "obj-task", "opt-high")
		fx.mwMock.EXPECT().RelationListRemoveOption(mock.Anything, mock.Anything).
			Return(&pb.RpcRelationListRemoveOptionResponse{})

		// when
		_, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "priority", "opt-high", true, false)

		// then
		require.NoError(t, err)
	})

	t.Run("a dry run reports the removal without calling the RPC", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		result, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "priority", "Low", false, true)

		// then
		require.NoError(t, err)
		assert.True(t, result.DryRun)
	})

	t.Run("an unknown option is a 404 naming the vocabulary", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		_, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "priority", "Urgent", false, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusNotFound, apiErr.Status)
		require.Len(t, apiErr.Issues, 1)
		assert.Contains(t, apiErr.Issues[0].Message, `"High"`)
		require.Len(t, apiErr.Issues[0].SeeAlso, 1, "the repair names the operation, not a route string")
	})

	t.Run("a property that carries no options is a 400", func(t *testing.T) {
		// given
		fx := newV2Fixture(t)
		fx.addRelation(t, testSpaceId, objectstore.TestObject{
			bundle.RelationKeyId:           domain.String("rel-note"),
			bundle.RelationKeyRelationKey:  domain.String("note"),
			bundle.RelationKeyApiObjectKey: domain.String("note"),
			bundle.RelationKeyName:         domain.String("Note"),
		})

		// when
		_, err := fx.RemovePropertyOption(context.Background(), testSpaceId, "note", "High", false, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.Status)
		assert.Equal(t, v2model.CodeValidationFailed, apiErr.Code)
	})
}

func TestV2SetPropertyOptionsOrder(t *testing.T) {
	t.Run("the write carries the requested order and answers with the stored one", func(t *testing.T) {
		// given: the stored order is Low then High, the request the reverse
		fx := seedPriorityProperty(t)
		var got *pb.RpcRelationOptionSetOrderRequest
		fx.mwMock.EXPECT().RelationOptionSetOrder(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcRelationOptionSetOrderRequest) *pb.RpcRelationOptionSetOrderResponse {
				got = req
				return &pb.RpcRelationOptionSetOrderResponse{}
			})

		// when
		result, err := fx.SetPropertyOptionsOrder(context.Background(), testSpaceId, "priority",
			v2model.SetPropertyOptionsOrderRequest{Options: []string{"High", "Low"}}, false)

		// then
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, testSpaceId, got.SpaceId)
		assert.Equal(t, "priority", got.RelationKey)
		assert.Equal(t, []string{"opt-high", "opt-low"}, got.RelationOptionOrder)
		assert.Equal(t, []v2model.OptionRow{
			{Id: "opt-low", Name: "Low"},
			{Id: "opt-high", Name: "High", Color: "red"},
		}, result.Options, "the answer is the store's order, not the request echoed back")
	})

	t.Run("an order that leaves an option out is refused", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		_, err := fx.SetPropertyOptionsOrder(context.Background(), testSpaceId, "priority",
			v2model.SetPropertyOptionsOrderRequest{Options: []string{"High"}}, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.Status)
		require.Len(t, apiErr.Issues, 1)
		assert.Contains(t, apiErr.Issues[0].Message, `"Low"`)
		require.Len(t, apiErr.Issues[0].SeeAlso, 1, "the repair names the operation")
	})

	t.Run("an option named twice is refused", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		_, err := fx.SetPropertyOptionsOrder(context.Background(), testSpaceId, "priority",
			v2model.SetPropertyOptionsOrderRequest{Options: []string{"High", "Low", "high"}}, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.Status)
		require.Len(t, apiErr.Issues, 1)
		assert.Equal(t, "/options/2", apiErr.Issues[0].Path)
	})

	t.Run("a dry run validates without calling the RPC", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		result, err := fx.SetPropertyOptionsOrder(context.Background(), testSpaceId, "priority",
			v2model.SetPropertyOptionsOrderRequest{Options: []string{"Low", "High"}}, true)

		// then
		require.NoError(t, err)
		assert.True(t, result.DryRun)
		assert.Equal(t, []v2model.OptionRow{
			{Id: "opt-low", Name: "Low"},
			{Id: "opt-high", Name: "High", Color: "red"},
		}, result.Options)
	})
}

func TestV2ListPropertiesWithValue(t *testing.T) {
	t.Run("the properties carrying a value are served with a count", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)
		var got *pb.RpcRelationListWithValueRequest
		fx.mwMock.EXPECT().RelationListWithValue(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcRelationListWithValueRequest) *pb.RpcRelationListWithValueResponse {
				got = req
				return &pb.RpcRelationListWithValueResponse{List: []*pb.RpcRelationListWithValueResponseResponseItem{
					{RelationKey: "priority", Counter: 2},
					{RelationKey: "unknown_key", Counter: 1},
				}}
			})

		// when
		rows, total, hasMore, err := fx.ListPropertiesWithValue(context.Background(), testSpaceId, "obj1", 0, 25)

		// then
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.False(t, hasMore)
		require.NotNil(t, got)
		assert.Equal(t, "obj1", got.Value.GetStringValue())
		assert.Equal(t, []v2model.RelationValueRow{
			{Key: "priority", Name: "Priority", Format: "select", Count: 2},
			// a key no live property answers to is still reported: the count
			// is real
			{Key: "unknown_key", Count: 1},
		}, rows)
	})

	t.Run("a NULL code carrying a description is the failure", func(t *testing.T) {
		// given: this RPC reports its one failure with the success code
		fx := seedPriorityProperty(t)
		fx.mwMock.EXPECT().RelationListWithValue(mock.Anything, mock.Anything).
			Return(&pb.RpcRelationListWithValueResponse{
				Error: &pb.RpcRelationListWithValueResponseError{
					Code:        pb.RpcRelationListWithValueResponseError_NULL,
					Description: "space is not loaded",
				},
			})

		// when
		_, _, _, err := fx.ListPropertiesWithValue(context.Background(), testSpaceId, "obj1", 0, 25)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Status)
		assert.Contains(t, apiErr.Message, "space is not loaded")
	})

	t.Run("an empty value is a 400", func(t *testing.T) {
		// given
		fx := seedPriorityProperty(t)

		// when
		_, _, _, err := fx.ListPropertiesWithValue(context.Background(), testSpaceId, " ", 0, 25)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.Status)
		assert.Equal(t, v2model.CodeValidationFailed, apiErr.Code)
	})
}

func TestV2ListTypeConflicts(t *testing.T) {
	seedPlantType := func(t *testing.T) *v2Fixture {
		t.Helper()
		fx := newV2Fixture(t)
		fx.addRelation(t, testSpaceId, objectstore.TestObject{
			bundle.RelationKeyId:             domain.String("rel-height"),
			bundle.RelationKeyRelationKey:    domain.String("height"),
			bundle.RelationKeyApiObjectKey:   domain.String("height"),
			bundle.RelationKeyName:           domain.String("Height"),
			bundle.RelationKeyRelationFormat: domain.Int64(int64(model.RelationFormat_number)),
		})
		fx.addType(t, testSpaceId, objectstore.TestObject{
			bundle.RelationKeyId:           domain.String(typeOpsTypeId),
			bundle.RelationKeyUniqueKey:    domain.String("ot-plant"),
			bundle.RelationKeyApiObjectKey: domain.String("plant"),
			bundle.RelationKeyName:         domain.String("Plant"),
		})
		return fx
	}

	t.Run("relation ids become property rows, and a dead id is skipped", func(t *testing.T) {
		// given
		fx := seedPlantType(t)
		fx.mwMock.EXPECT().ObjectTypeListConflictingRelations(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcObjectTypeListConflictingRelationsRequest) *pb.RpcObjectTypeListConflictingRelationsResponse {
				assert.Equal(t, typeOpsTypeId, req.TypeObjectId)
				return &pb.RpcObjectTypeListConflictingRelationsResponse{
					RelationIds: []string{"rel-height", "rel-nonexistent"},
				}
			})

		// when
		rows, total, hasMore, err := fx.ListTypeConflicts(context.Background(), testSpaceId, "plant", 0, 25)

		// then
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.False(t, hasMore)
		assert.Equal(t, []v2model.PropertyRow{{Key: "height", Name: "Height", Format: "number"}}, rows)
	})

	t.Run("a readonly bundled type is a 403", func(t *testing.T) {
		// given
		fx := seedPlantType(t)
		fx.mwMock.EXPECT().ObjectTypeListConflictingRelations(mock.Anything, mock.Anything).
			Return(&pb.RpcObjectTypeListConflictingRelationsResponse{
				Error: &pb.RpcObjectTypeListConflictingRelationsResponseError{
					Code:        pb.RpcObjectTypeListConflictingRelationsResponseError_READONLY_OBJECT_TYPE,
					Description: "type is bundled",
				},
			})

		// when
		_, _, _, err := fx.ListTypeConflicts(context.Background(), testSpaceId, "plant", 0, 25)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusForbidden, apiErr.Status)
		assert.Equal(t, v2model.CodeForbidden, apiErr.Code)
	})
}

func TestV2ResolveTypeConflicts(t *testing.T) {
	seedPlantType := func(t *testing.T) *v2Fixture {
		t.Helper()
		fx := newV2Fixture(t)
		fx.addType(t, testSpaceId, objectstore.TestObject{
			bundle.RelationKeyId:           domain.String(typeOpsTypeId),
			bundle.RelationKeyUniqueKey:    domain.String("ot-plant"),
			bundle.RelationKeyApiObjectKey: domain.String("plant"),
			bundle.RelationKeyName:         domain.String("Plant"),
		})
		return fx
	}

	t.Run("a dry run validates without calling the RPC", func(t *testing.T) {
		// given
		fx := seedPlantType(t)

		// when
		result, err := fx.ResolveTypeConflicts(context.Background(), testSpaceId, "plant", true)

		// then
		require.NoError(t, err)
		assert.True(t, result.DryRun)
	})

	t.Run("the live run aligns the type's objects", func(t *testing.T) {
		// given
		fx := seedPlantType(t)
		fx.mwMock.EXPECT().ObjectTypeResolveLayoutConflicts(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcObjectTypeResolveLayoutConflictsRequest) *pb.RpcObjectTypeResolveLayoutConflictsResponse {
				assert.Equal(t, typeOpsTypeId, req.TypeObjectId)
				return &pb.RpcObjectTypeResolveLayoutConflictsResponse{}
			})

		// when
		_, err := fx.ResolveTypeConflicts(context.Background(), testSpaceId, "plant", false)

		// then
		require.NoError(t, err)
	})

	t.Run("an unknown type is a 404 before anything is written", func(t *testing.T) {
		// given
		fx := seedPlantType(t)

		// when
		_, err := fx.ResolveTypeConflicts(context.Background(), testSpaceId, "nope", false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusNotFound, apiErr.Status)
	})
}

func TestV2SetTypesOrder(t *testing.T) {
	seedTwoTypes := func(t *testing.T) *v2Fixture {
		t.Helper()
		fx := newV2Fixture(t)
		for _, tp := range []struct {
			id, uniqueKey, slug, name string
		}{
			{"type-plant", "ot-plant", "plant", "Plant"},
			{"type-machine", "ot-machine", "machine", "Machine"},
		} {
			fx.addType(t, testSpaceId, objectstore.TestObject{
				bundle.RelationKeyId:           domain.String(tp.id),
				bundle.RelationKeyUniqueKey:    domain.String(tp.uniqueKey),
				bundle.RelationKeyApiObjectKey: domain.String(tp.slug),
				bundle.RelationKeyName:         domain.String(tp.name),
			})
		}
		return fx
	}

	t.Run("the listed types are moved to the front in the given order", func(t *testing.T) {
		// given
		fx := seedTwoTypes(t)
		var got *pb.RpcObjectTypeSetOrderRequest
		fx.mwMock.EXPECT().ObjectTypeSetOrder(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, req *pb.RpcObjectTypeSetOrderRequest) *pb.RpcObjectTypeSetOrderResponse {
				got = req
				return &pb.RpcObjectTypeSetOrderResponse{}
			})

		// when
		result, err := fx.SetTypesOrder(context.Background(), testSpaceId,
			v2model.SetTypesOrderRequest{Types: []string{"machine", "plant"}}, false)

		// then
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, []string{"type-machine", "type-plant"}, got.TypeIds)
		assert.Equal(t, []v2model.TypeRow{
			{Key: "machine", Name: "Machine"},
			{Key: "plant", Name: "Plant"},
		}, result.Types)
	})

	t.Run("an unknown type refuses the whole order", func(t *testing.T) {
		// given
		fx := seedTwoTypes(t)

		// when
		_, err := fx.SetTypesOrder(context.Background(), testSpaceId,
			v2model.SetTypesOrderRequest{Types: []string{"machine", "nope"}}, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusNotFound, apiErr.Status)
	})

	t.Run("a repeated type is refused", func(t *testing.T) {
		// given
		fx := seedTwoTypes(t)

		// when
		_, err := fx.SetTypesOrder(context.Background(), testSpaceId,
			v2model.SetTypesOrderRequest{Types: []string{"plant", "plant"}}, false)

		// then
		apiErr := v2Err(t, err)
		assert.Equal(t, http.StatusBadRequest, apiErr.Status)
		require.Len(t, apiErr.Issues, 1)
		assert.Equal(t, "/types/1", apiErr.Issues[0].Path)
	})

	t.Run("a dry run validates without calling the RPC", func(t *testing.T) {
		// given
		fx := seedTwoTypes(t)

		// when
		result, err := fx.SetTypesOrder(context.Background(), testSpaceId,
			v2model.SetTypesOrderRequest{Types: []string{"plant"}}, true)

		// then
		require.NoError(t, err)
		assert.True(t, result.DryRun)
		assert.Equal(t, []v2model.TypeRow{{Key: "plant", Name: "Plant"}}, result.Types)
	})
}
