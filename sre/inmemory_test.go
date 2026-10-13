package sre

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	te "github.com/caerus-dev/caerus-sdk-go/internal/testengine"
)

func seeded(resources ...MockResourceSeed) *InMemoryClient {
	return NewInMemoryClient(&MockOptions{Resources: resources})
}

func available(t *testing.T, mock *InMemoryClient, key string) (int, int) {
	t.Helper()
	resource := te.Must(mock.GetResource(ctx, key))
	return resource.AvailableAmount, resource.PendingCount
}

func TestTheMockIsUsableWhereverTheRealClientIs(t *testing.T) {
	var api API = NewInMemoryClient(nil)
	te.Must(api.CreateUnitary(ctx, "butaca", "seat", nil))
	te.NoError(t, api.Close())
}

func TestMovesUnitsFromAvailableToPendingWhenTaken(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "general", AvailableAmount: 5})
	te.Must(mock.Pooled("general").TakeMany(ctx, 2, nil))
	free, pending := available(t, mock, "general")
	te.Equal(t, free, 3)
	te.Equal(t, pending, 2)
}

func TestGivesThemBackOnRelease(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "general", AvailableAmount: 5})
	holder := te.Must(mock.Pooled("general").TakeMany(ctx, 2, nil))
	te.NoError(t, mock.Release(ctx, holder.ID))
	free, pending := available(t, mock, "general")
	te.Equal(t, free, 5)
	te.Equal(t, pending, 0)
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusReleased)
}

func TestKeepsThemTakenOnConfirm(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "general", AvailableAmount: 5})
	holder := te.Must(mock.Pooled("general").TakeMany(ctx, 2, nil))
	confirmed := te.Must(mock.Confirm(ctx, holder.ID, &ConfirmOptions{Metadata: map[string]any{"paymentId": "p"}}))
	te.Equal(t, confirmed.Status, StatusConfirmed)
	te.Equal(t, confirmed.Metadata, map[string]any{"paymentId": "p"})
	free, pending := available(t, mock, "general")
	te.Equal(t, free, 3)
	te.Equal(t, pending, 0)
	te.As[*HolderNotActiveError](t, mock.Release(ctx, holder.ID))
}

func TestRefusesToTakeMoreThanExistsAsTheEngineDoes(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "seat", AvailableAmount: 1})
	te.Must(mock.Unitary("seat").Take(ctx, nil))
	_, err := mock.Unitary("seat").Take(ctx, nil)
	outOfStock := te.As[*OutOfStockError](t, err)
	te.As[*ConflictError](t, err)
	te.Equal(t, outOfStock.Code(), caerus.CodeConflict)
	_, err = seeded(MockResourceSeed{Key: "g", AvailableAmount: 2}).Pooled("g").TakeMany(ctx, 3, nil)
	te.As[*OutOfStockError](t, err)
}

func TestDoesNotKnowAboutResourcesNobodyCreated(t *testing.T) {
	_, err := NewInMemoryClient(nil).Unitary("ghost").Take(ctx, nil)
	te.As[*ResourceNotFoundError](t, err)
}

func TestCreatesResourcesThroughTheAPIAndRefusesTheSameKeyTwice(t *testing.T) {
	mock := NewInMemoryClient(nil)
	resource := te.Must(mock.CreateMultiple(ctx, "entrada", "general", 10, &CreateResourceOptions{GroupKey: "f1", Metadata: map[string]any{"z": "a"}}))
	te.Equal(t, resource.AvailableAmount, 10)
	te.Equal(t, resource.TemplateID, "tpl-entrada")
	te.Equal(t, resource.GroupKey, "f1")
	_, err := mock.CreateUnitary(ctx, "entrada", "general", nil)
	te.As[*ConflictError](t, err)
}

func TestGivesHoldersARealExpiresAtAndExpiresNothingOnItsOwn(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "seat", AvailableAmount: 1})
	holder := te.Must(mock.Unitary("seat").Take(ctx, nil))
	te.True(t, holder.ExpiresAt.After(time.Now().Add(299*time.Second)), "expires too soon: %v", holder.ExpiresAt)
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusPending)
}

func TestExpiresWhatIsPastDueWhenTheClockMovesAndReturnsTheUnits(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 3})
	short := te.Must(mock.Pooled("g").Take(ctx, &TakeOptions{TTL: 10 * time.Second}))
	long := te.Must(mock.Pooled("g").Take(ctx, &TakeOptions{TTL: 100 * time.Second}))
	te.NoError(t, mock.AdvanceTime(10*time.Second))
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, short.ID)).Status, StatusExpired)
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, long.ID)).Status, StatusPending)
	free, pending := available(t, mock, "g")
	te.Equal(t, free, 2)
	te.Equal(t, pending, 1)
	_, err := mock.Confirm(ctx, short.ID, nil)
	te.As[*HolderNotActiveError](t, err)
}

func TestExpiresOneHolderOnDemand(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "seat", AvailableAmount: 1})
	holder := te.Must(mock.Unitary("seat").Take(ctx, nil))
	te.NoError(t, mock.Expire(holder.ID))
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusExpired)
	te.As[*HolderNotActiveError](t, mock.Expire(holder.ID))
}

func TestExtendsTheExpiryRoundingASubSecondExtensionUp(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "seat", AvailableAmount: 1})
	holder := te.Must(mock.Unitary("seat").Take(ctx, nil))
	extended := te.Must(mock.Extend(ctx, holder.ID, 1500*time.Millisecond))
	te.Equal(t, extended.ExpiresAt.Sub(holder.ExpiresAt), 2*time.Second)
	extended = te.Must(mock.Extend(ctx, holder.ID, time.Millisecond))
	te.Equal(t, extended.ExpiresAt.Sub(holder.ExpiresAt), 3*time.Second)
	_, err := mock.Extend(ctx, holder.ID, 0)
	te.As[*ValidationError](t, err)
}

func TestRefusesANegativeJump(t *testing.T) {
	te.As[*ValidationError](t, NewInMemoryClient(nil).AdvanceTime(-time.Second))
}

func TestFailsTheNextCallsToTheNamedMethodOnePerCall(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	first := NewTimeoutError("first", nil)
	mock.FailNext(MockTake, first)
	mock.FailNext(MockTake, NewOutOfStockError("second", nil))
	_, err := mock.Pooled("g").Take(ctx, nil)
	te.True(t, errors.Is(err, first), "expected the first failure")
	_, err = mock.Pooled("g").Take(ctx, nil)
	te.As[*OutOfStockError](t, err)
	te.Must(mock.Pooled("g").Take(ctx, nil))
}

func TestTakeManyFailsSeparatelyFromTakeAndFailuresCanBeForgotten(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	mock.FailNext(MockTakeMany, NewTimeoutError("x", nil))
	te.Must(mock.Pooled("g").Take(ctx, nil))
	mock.ClearFailures()
	te.Must(mock.Pooled("g").TakeMany(ctx, 2, nil))
}

func TestLetsACallerExerciseAReleaseThatFails(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	holder := te.Must(mock.Pooled("g").Take(ctx, nil))
	mock.FailNext(MockRelease, NewTimeoutError("slow", nil))
	te.As[*TimeoutError](t, mock.Release(ctx, holder.ID))
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusPending)
}

func TestGivesBackTheFirstHolderForARepeatedIdempotencyKey(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	first := te.Must(mock.Pooled("g").TakeMany(ctx, 2, &TakeOptions{IdempotencyKey: "order-1"}))
	again := te.Must(mock.Pooled("g").TakeMany(ctx, 2, &TakeOptions{IdempotencyKey: "order-1"}))
	te.Equal(t, again.ID, first.ID)
	free, _ := available(t, mock, "g")
	te.Equal(t, free, 3)
	te.NoError(t, mock.Release(ctx, first.ID))
	_, err := mock.Pooled("g").TakeMany(ctx, 2, &TakeOptions{IdempotencyKey: "order-1"})
	te.Contains(t, te.As[*HolderNotActiveError](t, err).Error(), "as RELEASED")
}

func TestKeepsMetadataAsAnObjectAndCopiesIt(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	given := map[string]any{"orderId": "A-1"}
	holder := te.Must(mock.Pooled("g").Take(ctx, &TakeOptions{Metadata: given}))
	given["orderId"] = "changed"
	holder.Metadata["orderId"] = "changed too"
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Metadata, map[string]any{"orderId": "A-1"})
}

func TestRejectsCallsAfterBeingClosed(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	te.NoError(t, mock.Close())
	_, err := mock.GetResource(ctx, "g")
	te.Contains(t, te.As[*caerus.Error](t, err).Error(), "has been closed")
}

func TestShowsItsStateForAssertionsTheAPICannotMake(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "a", AvailableAmount: 1}, MockResourceSeed{Key: "b", AvailableAmount: 2, GroupKey: "g", TemplateID: "tpl-x"})
	te.Must(mock.Pooled("b").Take(ctx, nil))
	snapshot := mock.Snapshot()
	te.Equal(t, len(snapshot.Resources), 2)
	te.Equal(t, snapshot.Resources[0].Key, "a")
	te.Equal(t, snapshot.Resources[0].TemplateID, "tpl-mock")
	te.Equal(t, snapshot.Resources[1].TemplateID, "tpl-x")
	te.Equal(t, len(snapshot.Holders), 1)
	snapshot.Resources[0].AvailableAmount = 99
	te.Equal(t, mock.Snapshot().Resources[0].AvailableAmount, 1)
}

func TestPagesAGroup(t *testing.T) {
	mock := NewInMemoryClient(&MockOptions{DefaultPageSize: 2})
	for _, key := range []string{"a", "b", "c"} {
		te.Must(mock.CreateUnitary(ctx, "t", key, &CreateResourceOptions{GroupKey: "g"}))
	}
	te.Must(mock.CreateUnitary(ctx, "t", "other", &CreateResourceOptions{GroupKey: "h"}))
	first := te.Must(mock.GetResourcesByGroup(ctx, "g", nil))
	te.Equal(t, len(first.Resources), 2)
	te.Equal(t, first.HasNextPage, true)
	second := te.Must(mock.GetResourcesByGroup(ctx, "g", &GetResourcesByGroupOptions{Page: 1}))
	te.Equal(t, len(second.Resources), 1)
	te.Equal(t, second.Resources[0].Key, "c")
	te.Equal(t, second.HasNextPage, false)
}

func TestUpdatesTheAvailableAmountAndRefusesNegativeStock(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	updated := te.Must(mock.UpdateResource(ctx, "g", -2, &UpdateResourceOptions{GroupKey: "new"}))
	te.Equal(t, updated.AvailableAmount, 3)
	te.Equal(t, updated.GroupKey, "new")
	_, err := mock.UpdateResource(ctx, "g", -4, nil)
	te.As[*ConflictError](t, err)
}

func TestDeletesAResourceOnlyWithoutPendingHolders(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	holder := te.Must(mock.Pooled("g").Take(ctx, nil))
	te.As[*ResourceHasActiveHoldsError](t, mock.DeleteResource(ctx, "g"))
	te.NoError(t, mock.Release(ctx, holder.ID))
	te.NoError(t, mock.DeleteResource(ctx, "g"))
	_, err := mock.GetResource(ctx, "g")
	te.As[*ResourceNotFoundError](t, err)
}

func TestListsHoldersNewestFirstNarrowingAndPaging(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "a", AvailableAmount: 5}, MockResourceSeed{Key: "b", AvailableAmount: 5})
	first := te.Must(mock.Pooled("a").Take(ctx, nil))
	second := te.Must(mock.Pooled("b").Take(ctx, nil))
	third := te.Must(mock.Pooled("a").Take(ctx, nil))
	te.NoError(t, mock.Release(ctx, third.ID))
	all := te.Must(mock.ListResourceHolders(ctx, nil))
	te.Equal(t, []string{all.Holders[0].ID, all.Holders[1].ID, all.Holders[2].ID}, []string{third.ID, second.ID, first.ID})
	oldest := te.Must(mock.ListResourceHolders(ctx, &ListResourceHoldersOptions{Sort: OldestFirst}))
	te.Equal(t, oldest.Holders[0].ID, first.ID)
	pending := te.Must(mock.ListResourceHolders(ctx, &ListResourceHoldersOptions{Status: StatusPending}))
	te.Equal(t, len(pending.Holders), 2)
	onA := te.Must(mock.ListResourceHolders(ctx, &ListResourceHoldersOptions{ResourceKey: "a"}))
	te.Equal(t, len(onA.Holders), 2)
	paged := te.Must(mock.ListResourceHolders(ctx, &ListResourceHoldersOptions{PageSize: 2}))
	te.Equal(t, len(paged.Holders), 2)
	te.Equal(t, paged.HasNextPage, true)
}

func TestNeverSellsTheSameUnitTwiceUnderConcurrency(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 50})
	var sold, refused atomic.Int32
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mock.Pooled("g").Take(ctx, nil); err == nil {
				sold.Add(1)
			} else {
				refused.Add(1)
			}
		}()
	}
	wg.Wait()
	te.Equal(t, sold.Load(), int32(50))
	te.Equal(t, refused.Load(), int32(150))
}

func TestStampsAndMovesTimestamps(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	created := te.Must(mock.GetResource(ctx, "g"))
	te.True(t, !created.CreatedAt.IsZero() && created.CreatedAt.Equal(created.UpdatedAt), "bad stamps %v %v", created.CreatedAt, created.UpdatedAt)
	te.NoError(t, mock.AdvanceTime(60*time.Second))
	updated := te.Must(mock.UpdateResource(ctx, "g", 1, nil))
	te.Equal(t, updated.UpdatedAt.Sub(created.UpdatedAt), 60*time.Second)
	te.Equal(t, updated.CreatedAt, created.CreatedAt)
	holder := te.Must(mock.Pooled("g").Take(ctx, nil))
	te.Equal(t, holder.CreatedAt, updated.UpdatedAt)
}

func TestTheMockAppliesTheSameValidationAsTheClient(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 5})
	_, err := mock.Pooled("g").TakeMany(ctx, 0, nil)
	te.As[*ValidationError](t, err)
	_, err = mock.Pooled("g").Take(ctx, &TakeOptions{TTL: -time.Second})
	te.As[*ValidationError](t, err)
	_, err = mock.CreateMultiple(ctx, "t", "k", 0, nil)
	te.As[*ValidationError](t, err)
	_, err = mock.Unitary("").Take(ctx, nil)
	te.As[*ValidationError](t, err)
}

func TestAdvancingTheClockKeepsTheFractionsOfASecond(t *testing.T) {
	mock := seeded(MockResourceSeed{Key: "g", AvailableAmount: 1})
	holder := te.Must(mock.Pooled("g").Take(ctx, &TakeOptions{TTL: time.Second}))
	te.NoError(t, mock.AdvanceTime(500*time.Millisecond))
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusPending)
	te.NoError(t, mock.AdvanceTime(500*time.Millisecond))
	te.Equal(t, te.Must(mock.GetResourceHolder(ctx, holder.ID)).Status, StatusExpired)
}

func TestNestedMetadataIsNotSharedWithTheCaller(t *testing.T) {
	mock := NewInMemoryClient(nil)
	given := map[string]any{"zona": map[string]any{"fila": "A"}, "asientos": []any{"A1", "A2"}}
	resource := te.Must(mock.CreateUnitary(ctx, "butaca", "seat", &CreateResourceOptions{Metadata: given}))
	given["zona"].(map[string]any)["fila"] = "B"
	resource.Metadata["zona"].(map[string]any)["fila"] = "C"
	resource.Metadata["asientos"].([]any)[0] = "Z9"
	stored := te.Must(mock.GetResource(ctx, "seat")).Metadata
	te.Equal(t, stored, map[string]any{"zona": map[string]any{"fila": "A"}, "asientos": []any{"A1", "A2"}})
}
