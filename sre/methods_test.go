package sre

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/internal/proto/srepb"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

var ctx = context.Background()

func lastTake(t *testing.T, engine *te.SRE) *srepb.TakeRequest {
	t.Helper()
	return engine.LastRequest().(*srepb.TakeRequest)
}

func holderWith(status srepb.ResourceHolderResponse_ResourceHolderStatus) func(context.Context, *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
	return func(context.Context, *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = status
		return holder, nil
	}
}

func TestTakeAsksForExactlyOneUnit(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.Unitary("seat_A12").Take(ctx, nil))
	te.Equal(t, lastTake(t, engine).GetResourceKey(), "seat_A12")
	te.Equal(t, lastTake(t, engine).GetAmount(), int32(1))
	te.Must(client.Pooled("general").Take(ctx, nil))
	te.Equal(t, lastTake(t, engine).GetAmount(), int32(1))
}

func TestTakePassesTheOptionsThrough(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).Pooled("general").Take(ctx, &TakeOptions{
		IdempotencyKey: "order-1",
		TTL:            90 * time.Second,
		Metadata:       map[string]any{"orderId": "A-1"},
	}))
	settings := lastTake(t, engine).GetSettings()
	te.Equal(t, settings.GetIdempotencyKey(), "order-1")
	te.Equal(t, settings.GetCustomTtlSeconds(), int32(90))
	te.Equal(t, settings.GetMetadata(), `{"orderId":"A-1"}`)
}

func TestTakeLeavesUnsetOptionsOffTheWire(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).Pooled("general").Take(ctx, nil))
	settings := lastTake(t, engine).GetSettings()
	te.True(t, settings.IdempotencyKey == nil, "idempotency key sent")
	te.True(t, settings.CustomTtlSeconds == nil, "ttl sent")
	te.True(t, settings.Metadata == nil, "metadata sent")
}

func TestATTLBelowASecondRoundsUpToOne(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).Pooled("general").Take(ctx, &TakeOptions{TTL: 1500 * time.Millisecond}))
	te.Equal(t, lastTake(t, engine).GetSettings().GetCustomTtlSeconds(), int32(2))
}

func TestRefusesToTakeWithoutAKeyAndCallsNothing(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	for _, key := range []string{"", "   "} {
		_, err := client.Unitary(key).Take(ctx, nil)
		te.As[*ValidationError](t, err)
		_, err = client.Pooled(key).TakeMany(ctx, 2, nil)
		te.As[*ValidationError](t, err)
	}
	te.Equal(t, engine.Calls("take"), 0)
}

func TestBuildingAHandleCallsNothing(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	client.Unitary("a")
	client.Pooled("b")
	te.Equal(t, engine.LastMethod(), "")
}

func TestTheHandleRemembersItsKey(t *testing.T) {
	client := connect(t, te.StartSRE(t))
	te.Equal(t, client.Unitary("seat_A12").Key(), "seat_A12")
	te.Equal(t, client.Pooled("general").Key(), "general")
	te.Equal(t, client.Pooled("general").String(), "PooledResource[general]")
}

func TestRejectsANonPositiveTTL(t *testing.T) {
	engine := te.StartSRE(t)
	_, err := connect(t, engine).Pooled("general").Take(ctx, &TakeOptions{TTL: -time.Second})
	te.Contains(t, te.As[*ValidationError](t, err).Error(), "TTL")
	te.Equal(t, engine.Calls("take"), 0)
}

func TestTakeManyAsksForTheAmountItWasGiven(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.Pooled("general").TakeMany(ctx, 4, nil))
	te.Equal(t, lastTake(t, engine).GetAmount(), int32(4))
	for _, amount := range []int{0, -1} {
		_, err := client.Pooled("general").TakeMany(ctx, amount, nil)
		te.As[*ValidationError](t, err)
	}
}

func TestThrowsWhenTheEngineReportsExpired(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", holderWith(srepb.ResourceHolderResponse_EXPIRED))
	_, err := connect(t, engine).Pooled("general").Take(ctx, nil)
	te.Contains(t, te.As[*ConflictError](t, err).Error(), "as EXPIRED, which this call cannot use")
}

func TestReturnsAQueuedHolderWithTheStatusVisible(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", holderWith(srepb.ResourceHolderResponse_QUEUED))
	holder := te.Must(connect(t, engine).Pooled("general").Take(ctx, nil))
	te.Equal(t, holder.Status, StatusQueued)
}

func TestReturnsPendingOnTheHappyPath(t *testing.T) {
	holder := te.Must(connect(t, te.StartSRE(t)).Pooled("general").Take(ctx, nil))
	te.Equal(t, *holder, ResourceHolder{
		ID: "hld-1", ResourceID: "res-1", Status: StatusPending, Amount: 1, ExpiresAt: time.Unix(1_785_164_400, 0),
	})
}

func TestDoesNotThrowWhenAQueryFindsAnExpiredHolder(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResourceHolder", func(context.Context, *srepb.GetResourceHolderRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = srepb.ResourceHolderResponse_EXPIRED
		return holder, nil
	})
	holder := te.Must(connect(t, engine).GetResourceHolder(ctx, "hld-1"))
	te.Equal(t, holder.Status, StatusExpired)
}

func TestRefusesToGuessAtAStatusItDoesNotKnow(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResourceHolder", func(context.Context, *srepb.GetResourceHolderRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = srepb.ResourceHolderResponse_ResourceHolderStatus(42)
		return holder, nil
	})
	_, err := connect(t, engine).GetResourceHolder(ctx, "hld-1")
	base := te.As[*caerus.Error](t, err)
	te.Contains(t, base.Error(), "unknown status (42)")
	te.Contains(t, base.Error(), "Update github.com/caerus-dev/caerus-sdk-go")
	te.NotAs[*ConflictError](t, err)
}

func TestMetadataGoesOutAsJSONTextAndComesBackAsAnObject(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", func(_ context.Context, request *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Metadata = request.GetSettings().Metadata
		return holder, nil
	})
	sent := map[string]any{"orderId": "A-1", "items": float64(2), "nested": map[string]any{"vip": true}, "html": "<b>&</b>"}
	holder := te.Must(connect(t, engine).Pooled("general").Take(ctx, &TakeOptions{Metadata: sent}))
	te.Equal(t, holder.Metadata, sent)
	te.Contains(t, lastTake(t, engine).GetSettings().GetMetadata(), `"html":"<b>&</b>"`)
}

func TestReadsAMissingOrBlankMetadataAsNone(t *testing.T) {
	for _, raw := range []*string{nil, proto.String(""), proto.String("   ")} {
		engine := te.StartSRE(t)
		te.OnSRE(engine, "getResource", func(context.Context, *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
			resource := te.AResource()
			resource.Metadata = raw
			return resource, nil
		})
		resource := te.Must(connect(t, engine).GetResource(ctx, "seat_A12"))
		te.True(t, resource.Metadata == nil, "metadata should be absent")
	}
}

func TestMetadataThatIsNotAJSONObjectFailsShowingTheRawValue(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(context.Context, *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		resource := te.AResource()
		resource.Metadata = proto.String("[1,2]")
		return resource, nil
	})
	_, err := connect(t, engine).GetResource(ctx, "seat_A12")
	te.Contains(t, te.As[*caerus.Error](t, err).Error(), "Raw value: [1,2]")
}

func TestSendsNoMetadataWhenNoneWasGiven(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).CreateMultiple(ctx, "butaca", "seat", 2, nil))
	te.True(t, engine.LastRequest().(*srepb.CreateResourceRequest).Metadata == nil, "metadata sent")
}

func TestTurnsEpochSecondsIntoTheMatchingInstantAndDoesNotLandIn1970(t *testing.T) {
	holder := te.Must(connect(t, te.StartSRE(t)).Pooled("general").Take(ctx, nil))
	te.Equal(t, holder.ExpiresAt.UTC(), time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC))
	te.True(t, holder.ExpiresAt.Year() > 2000, "landed in %v", holder.ExpiresAt)
}

func TestCreateResourceSendsWhatTheEngineNeedsAndReturnsAResource(t *testing.T) {
	engine := te.StartSRE(t)
	resource := te.Must(connect(t, engine).CreateMultiple(ctx, "entrada", "general", 500, &CreateResourceOptions{
		GroupKey: "funcion_20",
		Metadata: map[string]any{"zona": "platea"},
	}))
	request := engine.LastRequest().(*srepb.CreateResourceRequest)
	te.Equal(t, request.GetTemplateName(), "entrada")
	te.Equal(t, request.GetKey(), "general")
	te.Equal(t, request.GetAvailableAmount(), int32(500))
	te.Equal(t, request.GetGroupKey(), "funcion_20")
	te.Equal(t, request.GetMetadata(), `{"zona":"platea"}`)
	te.Equal(t, resource.Key, "seat_A12")
	te.Equal(t, resource.TemplateID, "tpl-1")
}

func TestCreateUnitaryAsksForExactlyOne(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).CreateUnitary(ctx, "butaca", "seat_A12", nil))
	te.Equal(t, engine.LastRequest().(*srepb.CreateResourceRequest).GetAvailableAmount(), int32(1))
}

func TestCreateRefusesABlankTemplateKeyOrAmount(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	_, err := client.CreateUnitary(ctx, " ", "k", nil)
	te.Contains(t, te.As[*ValidationError](t, err).Error(), "templateName")
	_, err = client.CreateUnitary(ctx, "t", "", nil)
	te.Contains(t, te.As[*ValidationError](t, err).Error(), "key")
	_, err = client.CreateMultiple(ctx, "t", "k", 0, nil)
	te.Contains(t, te.As[*ValidationError](t, err).Error(), "availableAmount")
	te.Equal(t, engine.LastMethod(), "")
}

func TestConfirmSendsTheMetadataPatchAsJSONText(t *testing.T) {
	engine := te.StartSRE(t)
	holder := te.Must(connect(t, engine).Confirm(ctx, "hld-1", &ConfirmOptions{Metadata: map[string]any{"paymentId": "p-9"}}))
	request := engine.LastRequest().(*srepb.ConfirmRequest)
	te.Equal(t, request.GetResourceHolderId(), "hld-1")
	te.Equal(t, request.GetMetadataPatch(), `{"paymentId":"p-9"}`)
	te.Equal(t, holder.Status, StatusConfirmed)
}

func TestConfirmRefusesAHolderThatDidNotEndConfirmed(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "confirm", func(context.Context, *srepb.ConfirmRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = srepb.ResourceHolderResponse_RELEASED
		return holder, nil
	})
	_, err := connect(t, engine).Confirm(ctx, "hld-1", nil)
	te.Contains(t, te.As[*ConflictError](t, err).Error(), "as RELEASED")
}

func TestReleaseSendsTheHolder(t *testing.T) {
	engine := te.StartSRE(t)
	te.NoError(t, connect(t, engine).Release(ctx, "hld-1"))
	te.Equal(t, engine.LastRequest().(*srepb.ReleaseRequest).GetResourceHolderId(), "hld-1")
}

func TestExtendSendsTheExtensionInMilliseconds(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.Extend(ctx, "hld-1", 1500*time.Millisecond))
	te.Equal(t, engine.LastRequest().(*srepb.ExtendRequest).GetExtraMs(), int32(1500))
	for _, extra := range []time.Duration{0, -time.Second, time.Microsecond * 0} {
		_, err := client.Extend(ctx, "hld-1", extra)
		te.As[*ValidationError](t, err)
	}
}

func TestExtendAcceptsQueuedAndRefusesReleased(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "extend", func(context.Context, *srepb.ExtendRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = srepb.ResourceHolderResponse_QUEUED
		return holder, nil
	})
	client := connect(t, engine)
	te.Must(client.Extend(ctx, "hld-1", time.Second))
	te.OnSRE(engine, "extend", func(context.Context, *srepb.ExtendRequest) (*srepb.ResourceHolderResponse, error) {
		holder := te.AHolder()
		holder.Status = srepb.ResourceHolderResponse_RELEASED
		return holder, nil
	})
	_, err := client.Extend(ctx, "hld-1", time.Second)
	te.As[*ConflictError](t, err)
}

func TestReadsAResource(t *testing.T) {
	engine := te.StartSRE(t)
	resource := te.Must(connect(t, engine).GetResource(ctx, "seat_A12"))
	te.Equal(t, engine.LastRequest().(*srepb.GetResourceRequest).GetKey(), "seat_A12")
	te.Equal(t, *resource, Resource{ID: "res-1", Key: "seat_A12", TemplateID: "tpl-1", AvailableAmount: 1})
}

func TestReadsAPageOfAGroupAndSaysWhetherThereIsAnother(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResourcesByGroupKey", func(context.Context, *srepb.GetResourcesByGroupKeyRequest) (*srepb.GetResourcesByGroupKeyResponse, error) {
		return &srepb.GetResourcesByGroupKeyResponse{Resources: []*srepb.ResourceResponse{te.AResource(), te.AResource()}, NextPage: true}, nil
	})
	page := te.Must(connect(t, engine).GetResourcesByGroup(ctx, "funcion_20", &GetResourcesByGroupOptions{Page: 2, PageSize: 10}))
	request := engine.LastRequest().(*srepb.GetResourcesByGroupKeyRequest)
	te.Equal(t, request.GetGroupKey(), "funcion_20")
	te.Equal(t, request.GetPage(), int32(2))
	te.Equal(t, request.GetPageSize(), int32(10))
	te.Equal(t, len(page.Resources), 2)
	te.Equal(t, page.HasNextPage, true)
}

func TestStartsAtTheFirstPageWhenNoneIsAskedFor(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).GetResourcesByGroup(ctx, "g", nil))
	request := engine.LastRequest().(*srepb.GetResourcesByGroupKeyRequest)
	te.Equal(t, request.GetPage(), int32(0))
	te.True(t, request.PageSize == nil, "page size sent")
}

func TestListsHoldersAndSaysWhetherThereIsAnotherPage(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResourceHoldersList", func(context.Context, *srepb.GetResourceHoldersListRequest) (*srepb.GetResourceHoldersListResponse, error) {
		return &srepb.GetResourceHoldersListResponse{ResourceHolders: []*srepb.ResourceHolderResponse{te.AHolder()}, NextPage: true}, nil
	})
	page := te.Must(connect(t, engine).ListResourceHolders(ctx, &ListResourceHoldersOptions{ResourceKey: "seat", Page: 1, PageSize: 5}))
	request := engine.LastRequest().(*srepb.GetResourceHoldersListRequest)
	te.Equal(t, request.GetResourceKey(), "seat")
	te.Equal(t, request.GetPage(), int32(1))
	te.Equal(t, request.GetPageSize(), int32(5))
	te.Equal(t, len(page.Holders), 1)
	te.Equal(t, page.HasNextPage, true)
}

func TestListsNewestFirstUnlessAskedForTheOtherOrder(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.ListResourceHolders(ctx, nil))
	te.Equal(t, engine.LastRequest().(*srepb.GetResourceHoldersListRequest).GetCreatedAtSortDirection(), srepb.GetResourceHoldersListRequest_DESCENDING)
	te.Must(client.ListResourceHolders(ctx, &ListResourceHoldersOptions{Sort: OldestFirst}))
	te.Equal(t, engine.LastRequest().(*srepb.GetResourceHoldersListRequest).GetCreatedAtSortDirection(), srepb.GetResourceHoldersListRequest_ASCENDING)
}

func TestLeavesTheStatusFilterOutWhenNoneIsAskedFor(t *testing.T) {
	engine := te.StartSRE(t)
	te.Must(connect(t, engine).ListResourceHolders(ctx, nil))
	te.True(t, engine.LastRequest().(*srepb.GetResourceHoldersListRequest).StatusFilter == nil, "status sent")
}

func TestEncodesEveryStatusFilterTheWayTheEngineNumbersThem(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	expected := map[ResourceHolderStatus]int32{StatusPending: 0, StatusConfirmed: 1, StatusReleased: 2, StatusQueued: 3, StatusExpired: 4}
	for status, wire := range expected {
		te.Must(client.ListResourceHolders(ctx, &ListResourceHoldersOptions{Status: status}))
		te.Equal(t, int32(engine.LastRequest().(*srepb.GetResourceHoldersListRequest).GetStatusFilter()), wire)
	}
	_, err := client.ListResourceHolders(ctx, &ListResourceHoldersOptions{Status: "LOST"})
	te.As[*ValidationError](t, err)
}

func TestReadsAHolder(t *testing.T) {
	engine := te.StartSRE(t)
	holder := te.Must(connect(t, engine).GetResourceHolder(ctx, "hld-1"))
	te.Equal(t, engine.LastRequest().(*srepb.GetResourceHolderRequest).GetResourceHolderId(), "hld-1")
	te.Equal(t, holder.ID, "hld-1")
}

func TestRunningOutOfStockReachesTakeManyTheSameWay(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "take", func(context.Context, *srepb.TakeRequest) (*srepb.ResourceHolderResponse, error) {
		return nil, te.WithReason(codes.FailedPrecondition, "Out of stock for resource: general", "OUT_OF_STOCK")
	})
	_, err := connect(t, engine).Pooled("general").TakeMany(ctx, 3, nil)
	outOfStock := te.As[*OutOfStockError](t, err)
	te.Contains(t, outOfStock.Error(), "Out of stock for resource: general")
	te.Equal(t, outOfStock.Code(), caerus.CodeConflict)
}

func TestSendsUpdateResourceCorrectly(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	te.Must(client.UpdateResource(ctx, "general", 5, &UpdateResourceOptions{GroupKey: "g", Metadata: map[string]any{"a": "b"}, IdempotencyKey: "idem"}))
	request := engine.LastRequest().(*srepb.UpdateResourceRequest)
	te.Equal(t, request.GetResourceKey(), "general")
	te.Equal(t, request.GetDeltaAmount(), int32(5))
	te.Equal(t, request.GetGroupKey(), "g")
	te.Equal(t, request.GetMetadata(), `{"a":"b"}`)
	te.Equal(t, request.GetIdempotencyKey(), "idem")
	te.Must(client.UpdateResource(ctx, "general", -3, nil))
	request = engine.LastRequest().(*srepb.UpdateResourceRequest)
	te.Equal(t, request.GetDeltaAmount(), int32(-3))
	te.True(t, request.IdempotencyKey == nil && request.GroupKey == nil && request.Metadata == nil, "unset options sent")
}

func TestSendsDeleteResourceCorrectly(t *testing.T) {
	engine := te.StartSRE(t)
	te.NoError(t, connect(t, engine).DeleteResource(ctx, "general"))
	te.Equal(t, engine.LastRequest().(*srepb.DeleteResourceRequest).GetKey(), "general")
}

func TestAnEmptyGroupKeyReadsAsNone(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(context.Context, *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		resource := te.AResource()
		resource.GroupKey = proto.String("")
		return resource, nil
	})
	te.Equal(t, te.Must(connect(t, engine).GetResource(ctx, "x")).GroupKey, "")
}

func TestTheQueriesRefuseBlankArgumentsWithoutCalling(t *testing.T) {
	engine := te.StartSRE(t)
	client := connect(t, engine)
	_, err := client.GetResource(ctx, " ")
	te.As[*ValidationError](t, err)
	_, err = client.GetResourceHolder(ctx, "")
	te.As[*ValidationError](t, err)
	_, err = client.GetResourcesByGroup(ctx, "", nil)
	te.As[*ValidationError](t, err)
	_, err = client.Confirm(ctx, "", nil)
	te.As[*ValidationError](t, err)
	te.As[*ValidationError](t, client.Release(ctx, " "))
	te.As[*ValidationError](t, client.DeleteResource(ctx, ""))
	_, err = client.UpdateResource(ctx, "", 1, nil)
	te.As[*ValidationError](t, err)
	te.Equal(t, engine.LastMethod(), "")
}

func TestReadsTimestampsAndLeavesThemZeroWhenTheEngineSendsNone(t *testing.T) {
	engine := te.StartSRE(t)
	te.OnSRE(engine, "getResource", func(context.Context, *srepb.GetResourceRequest) (*srepb.ResourceResponse, error) {
		resource := te.AResource()
		resource.CreatedAtMs = 1_759_000_000_123
		resource.UpdatedAtMs = 1_759_000_005_000
		return resource, nil
	})
	resource := te.Must(connect(t, engine).GetResource(ctx, "x"))
	te.Equal(t, resource.CreatedAt.UnixMilli(), int64(1_759_000_000_123))
	te.Equal(t, resource.UpdatedAt.UnixMilli(), int64(1_759_000_005_000))
	holder := te.Must(connect(t, te.StartSRE(t)).GetResourceHolder(ctx, "hld-1"))
	te.True(t, holder.CreatedAt.IsZero(), "a missing timestamp must stay zero, not 1970")
}
