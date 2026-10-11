# Caerus SDK para Go

El cliente oficial de Caerus para Go. Reserva stock limitado (asientos, cupos, turnos,
inventario) y coordina locks distribuidos sin escribir la lógica de concurrencia.

```go
holder, err := caerus.Unitary("butaca_A12").Take(ctx, &sre.TakeOptions{IdempotencyKey: orderID})
if err != nil {
	return err
}
chargeCard(4500)
_, err = caerus.Confirm(ctx, holder.ID, nil)
```

Es el mismo producto que [`@caerus-dev/sdk`](https://github.com/caerus-dev/caerus-sdk-ts) y
[`io.github.caerus-dev:caerus-sdk`](https://github.com/caerus-dev/caerus-sdk-java): mismos
métodos, mismos errores y mismos bordes. Lo que cambia es lo que tiene que cambiar para que
se sienta Go: `context.Context` primero, errores que se miran con `errors.As` y opciones en
un struct. La equivalencia completa está en [docs/ts-a-go.md](docs/ts-a-go.md).

**Corre en tu servidor.** La API Key autentica cada llamada e identifica tu entorno, así
que nunca tiene que llegar a un cliente o a un navegador.

---

## Instalación

Requiere Go 1.25 o superior.

```bash
go get github.com/caerus-dev/caerus-sdk-go
```

Trae gRPC y protobuf. Nada de eso aparece en la API pública: un test lo hace cumplir.

## Configuración

```go
client, err := sre.NewClient(sre.ClientOptions{APIKey: os.Getenv("CAERUS_API_KEY")})
if err != nil {
	return err
}
defer client.Close()
```

Esa es toda la configuración. La API Key identifica el entorno, así que el cliente ya sabe
adónde conectarse.

Un cliente por proceso: la conexión multiplexa todas las llamadas y se puede usar desde
varias goroutines a la vez. `Close()` es idempotente y después de cerrar toda llamada falla
con un `*caerus.Error`.

| Opción | Default | Regla |
|---|---|---|
| `APIKey` | — | Obligatoria. Nunca se lee del entorno ni aparece al imprimir el cliente o las opciones |
| `Endpoint` | `CAERUS_ENDPOINT`, si no `caerus.dev.ar.sdk.apps.disilab.ar:443` | `host:puerto`, sin esquema |
| `TLS` | `CAERUS_TLS`, si no prendido | `caerus.Bool(false)` para un motor local. En la variable solo `false` o `0` lo apagan |
| `Timeout` | 10 s | Deadline de cada llamada. Si el `ctx` trae uno más corto, gana ese |
| `Logger` | stderr con prefijo `[caerus]` | Un `*slog.Logger`. Solo para lo que no conviene devolver como error |

`dls.NewClient` lee `CAERUS_DLS_ENDPOINT` y `CAERUS_DLS_TLS`, y su prefijo es
`[caerus-dls]`.

---

## Shared Resource Engine

```go
_, err := client.CreateMultiple(ctx, "entrada", "general", 500, &sre.CreateResourceOptions{GroupKey: "funcion_20"})

holder, err := client.Pooled("general").TakeMany(ctx, 2, &sre.TakeOptions{
	IdempotencyKey: orderID,
	TTL:            10 * time.Minute,
	Metadata:       map[string]any{"orderId": orderID},
})
if err != nil {
	return err
}
if err := pay(holder); err != nil {
	_ = client.Release(ctx, holder.ID)
	return err
}
_, err = client.Confirm(ctx, holder.ID, &sre.ConfirmOptions{Metadata: map[string]any{"paymentId": paymentID}})
```

| Método | Qué hace |
|---|---|
| `CreateUnitary(ctx, template, key, opts)` / `CreateMultiple(ctx, template, key, amount, opts)` | Declara un recurso |
| `UpdateResource(ctx, key, delta, opts)` | Suma o resta stock |
| `DeleteResource(ctx, key)` | Lo borra; falla si alguien lo tiene tomado |
| `Unitary(key).Take(ctx, opts)` | Toma la única unidad |
| `Pooled(key).Take(ctx, opts)` / `.TakeMany(ctx, n, opts)` | Toma una o varias |
| `Confirm(ctx, id, opts)` | Las deja tomadas para siempre |
| `Release(ctx, id)` | Las devuelve |
| `Extend(ctx, id, extra)` | Corre el vencimiento; el motor redondea hacia arriba a segundos |
| `GetResourceHolder(ctx, id)` | Lee un holder; es el único que devuelve un `EXPIRED` sin fallar |
| `GetResource(ctx, key)` / `GetResourcesByGroup(ctx, group, opts)` / `ListResourceHolders(ctx, opts)` | Consultas |

Las opciones van en un struct al final; `nil` es "sin opciones". `Unitary` y `Pooled` no
llaman al motor: solo arman el handle.

## Distributed Lock Service

```go
locks, err := dls.NewClient(dls.ClientOptions{APIKey: apiKey})
if err != nil {
	return err
}
defer locks.Close()

err = locks.WithTransaction(ctx, func(ctx context.Context, tx dls.Tx) error {
	lock, err := tx.AcquireLock(ctx, "cuentas", "cuenta-42", dls.Exclusive, &dls.AcquireLockOptions{
		IdempotencyKey: requestID,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return err
	}
	return transfer(ctx, lock.FencingToken)
}, &dls.TransactionOptions{Timeout: 20 * time.Second})
```

`WithTransaction` abre la transacción, la renueva sola mientras el callback trabaja y suelta
todos los locks al final, salga bien o salga mal (también si el callback entra en pánico).
Si la renovación falla sin remedio, la transacción se da por perdida: se cancela el `ctx`
del callback (con el error como causa, `context.Cause(ctx)`), se llama a
`OnTransactionLost` y `WithTransaction` devuelve ese error aunque el callback haya
terminado bien. El error del callback sale tal cual, sin envolver.

Un lock denegado nunca vuelve como valor: es `*dls.LockDeniedError`. Una espera en la cola se
corta cancelando el `ctx`, y `OnQueued` avisa una sola vez.

## Webhooks

```go
event, err := webhooks.ConstructEvent(rawBody, r.Header.Get(webhooks.SignatureHeader), secret, nil)
if err != nil {
	http.Error(w, "firma inválida", http.StatusBadRequest)
	return
}
if taken, ok := event.Data.(*webhooks.ResourceTakenData); ok {
	log.Printf("holder %s tomó %d unidades", taken.HolderID, taken.Amount)
}
```

Verifica la firma HMAC-SHA256 contra el cuerpo crudo, en tiempo constante, con 5 minutos de
tolerancia. Cada tipo de evento tiene su struct; un evento que el SDK todavía no conoce llega
como `*webhooks.UnknownEventData` en vez de romper al consumidor.

## Errores

Todo lo que devuelve el SDK se puede mirar como `*caerus.Error`, que trae `Code()` para un
`switch`, `Reason()` con el código del motor (`OUT_OF_STOCK`, `DEADLOCK_DETECTED`...) y
`RequestID()` para pedir soporte. Cada caso tiene además su tipo:

```go
var outOfStock *sre.OutOfStockError
if errors.As(err, &outOfStock) {
	return "no hay lugar"
}
```

Detalle en [docs/errores.md](docs/errores.md). El SDK no reintenta nada. Reintentar es
decisión de quien llama, con clave de idempotencia.

## Testing sin Caerus

`sre.InMemoryClient` y `dls.InMemoryClient` implementan `sre.API` y `dls.API`, así que el
código bajo test no se entera.

```go
caerus := sre.NewInMemoryClient(&sre.MockOptions{
	Resources: []sre.MockResourceSeed{{Key: "butaca_A12", AvailableAmount: 1}},
})
caerus.FailNext(sre.MockConfirm, sre.NewConflictError("pago rechazado", nil))
_ = caerus.AdvanceTime(10 * time.Minute)
```

El tiempo no pasa solo: lo mueve el test. Se pueden usar desde varias goroutines.

---

## Desarrollo

```bash
go vet ./...
go test ./...
```

Los tests corren contra un motor gRPC falso en `127.0.0.1`. El código generado desde `proto/`
está commiteado (ver [proto/README.md](proto/README.md)). La verificación contra un motor
real está en [docs/verificacion-en-vivo.md](docs/verificacion-en-vivo.md).

## Licencia

MIT
