//go:build live

package live

import (
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/caerus-dev/caerus-sdk-go"
	"github.com/caerus-dev/caerus-sdk-go/sre"
)

func template(t *testing.T) string {
	t.Helper()
	name := env("SRE_TEMPLATE", "")
	if name == "" {
		t.Skip("Falta SRE_TEMPLATE")
	}
	return name
}

func takeOptions(options *sre.TakeOptions) *sre.TakeOptions {
	if options == nil {
		options = &sre.TakeOptions{}
	}
	if options.IdempotencyKey == "" {
		options.IdempotencyKey = rand.Text()
	}
	return options
}

func TestTheWholeLifeOfAPooledResource(t *testing.T) {
	templateName := template(t)
	client := sreClient(t)
	resourceKey, group := key("pool"), key("grupo")
	created := must(client.CreateMultiple(ctx, templateName, resourceKey, 3, &sre.CreateResourceOptions{
		GroupKey: group,
		Metadata: map[string]any{"zona": "platea"},
	}))
	report(t, "crea el recurso", []any{created.Key, created.AvailableAmount, created.GroupKey})
	check(t, created.AvailableAmount == 3 && created.GroupKey == group, "created %+v", created)
	defer func() { _ = client.DeleteResource(ctx, resourceKey) }()

	same := rand.Text()
	first := must(client.Pooled(resourceKey).TakeMany(ctx, 2, &sre.TakeOptions{IdempotencyKey: same, Metadata: map[string]any{"orden": "A-1", "items": 2}}))
	replay := must(client.Pooled(resourceKey).TakeMany(ctx, 2, &sre.TakeOptions{IdempotencyKey: same}))
	report(t, "la misma clave devuelve el mismo holder", first.ID+" / "+replay.ID)
	check(t, first.Status == sre.StatusPending && replay.ID == first.ID, "replay %+v", replay)
	check(t, must(client.GetResource(ctx, resourceKey)).AvailableAmount == 1, "stock did not move")
	check(t, first.Metadata["orden"] == "A-1", "metadata %v", first.Metadata)

	report(t, "el vencimiento es una fecha razonable", first.ExpiresAt.UTC())
	check(t, first.ExpiresAt.After(time.Now()) && first.ExpiresAt.Before(time.Now().Add(48*time.Hour)), "expires %v", first.ExpiresAt)
	extended := must(client.Extend(ctx, first.ID, 1500*time.Millisecond))
	report(t, "extend redondea a segundos hacia arriba", extended.ExpiresAt.Sub(first.ExpiresAt))
	check(t, extended.ExpiresAt.Sub(first.ExpiresAt) == 2*time.Second, "moved %v", extended.ExpiresAt.Sub(first.ExpiresAt))

	second := must(client.Pooled(resourceKey).Take(ctx, takeOptions(nil)))
	_, outOfStock := client.Pooled(resourceKey).Take(ctx, takeOptions(nil))
	report(t, "sin stock llega OutOfStockError", outOfStock)
	var noStock *sre.OutOfStockError
	check(t, errors.As(outOfStock, &noStock) && noStock.Reason() == "OUT_OF_STOCK", "got %v", outOfStock)

	confirmed := must(client.Confirm(ctx, first.ID, &sre.ConfirmOptions{Metadata: map[string]any{"pago": "p-1"}}))
	report(t, "confirm devuelve CONFIRMED", confirmed.Status)
	check(t, confirmed.Status == sre.StatusConfirmed, "status %q", confirmed.Status)

	busy := client.DeleteResource(ctx, resourceKey)
	report(t, "no deja borrar con holders activos", busy)
	var activeHolds *sre.ResourceHasActiveHoldsError
	check(t, errors.As(busy, &activeHolds), "got %v", busy)

	ok(t, client.Release(ctx, second.ID))
	check(t, must(client.GetResourceHolder(ctx, second.ID)).Status == sre.StatusReleased, "not released")
	_, replayReleased := client.Extend(ctx, second.ID, time.Second)
	report(t, "un holder liberado no se puede extender", replayReleased)
	var base *caerus.Error
	check(t, errors.As(replayReleased, &base), "got %v", replayReleased)

	holders := must(client.ListResourceHolders(ctx, &sre.ListResourceHoldersOptions{ResourceKey: resourceKey}))
	ids := []string{}
	for _, holder := range holders.Holders {
		ids = append(ids, holder.ID)
	}
	report(t, "lista los holders del recurso", len(ids))
	check(t, slices.Contains(ids, first.ID) && slices.Contains(ids, second.ID), "holders %v", ids)

	page := waitForGroup(t, client, group)
	report(t, "el grupo encuentra el recurso", len(page.Resources))
	check(t, slices.ContainsFunc(page.Resources, func(resource sre.Resource) bool { return resource.Key == resourceKey }), "group %v", page.Resources)

	updated := must(client.UpdateResource(ctx, resourceKey, 4, &sre.UpdateResourceOptions{IdempotencyKey: rand.Text()}))
	report(t, "updateResource suma stock", updated.AvailableAmount)
	check(t, updated.AvailableAmount == must(client.GetResource(ctx, resourceKey)).AvailableAmount, "stock mismatch")
}

func TestAResourceThatDoesNotExistIsNotFound(t *testing.T) {
	_, err := sreClient(t).GetResource(ctx, key("no_existe"))
	report(t, "un recurso inexistente da ResourceNotFoundError", err)
	var notFound *sre.ResourceNotFoundError
	check(t, errors.As(err, &notFound), "got %v", err)
	check(t, notFound.RequestID() != "", "missing request id")
}

func TestAWrongAPIKeyIsAnAuthenticationErrorInTheSRE(t *testing.T) {
	apiKey(t)
	stranger := must(sre.NewClient(sre.ClientOptions{APIKey: "caer_dev_no_existe_" + rand.Text(), Endpoint: endpoint(), TLS: tls()}))
	defer stranger.Close()
	_, err := stranger.GetResource(ctx, "x")
	report(t, "una API Key que no existe da error de autenticacion", err)
	var auth *sre.AuthenticationError
	check(t, errors.As(err, &auth), "got %v", err)
}

func waitForGroup(t *testing.T, client *sre.Client, group string) *sre.ResourcePage {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	page := must(client.GetResourcesByGroup(ctx, group, nil))
	for len(page.Resources) == 0 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		page = must(client.GetResourcesByGroup(ctx, group, nil))
	}
	return page
}
