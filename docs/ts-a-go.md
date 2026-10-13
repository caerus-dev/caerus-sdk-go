# De TypeScript a Go

El SDK de Go es el mismo producto que `@caerus-dev/sdk` 0.5.0. Esta tabla dice cómo se llama
cada cosa y qué cambia por ser Go.

## Lo que cambia en todo el SDK

| TypeScript | Go | Por qué |
|---|---|---|
| `Promise<T>` | `(T, error)` bloqueante | En Go la concurrencia la pone quien llama, con goroutines |
| `AbortSignal` y `signal` en las opciones | `context.Context` como primer parámetro | Es como se cancela en Go; también acota el deadline |
| `throw` / `instanceof` | `error` / `errors.As` | Los tipos respetan la jerarquía de TS (ver [errores.md](errores.md)) |
| Opciones opcionales (`{ ... }`) | Struct al final, por puntero; `nil` = sin opciones | Misma regla del SDK: un objeto de opciones al final |
| Milisegundos (`timeoutMs`, `extraMs`) | `time.Duration` (`Timeout`, `extra`) | Se convierte redondeando hacia arriba al milisegundo; `TTL` al segundo |
| `undefined` | Valor cero (`""`, `0`, `time.Time{}`, `nil`) | El motor no distingue vacío de ausente en esos campos |
| `logger: { error }` | `Logger *slog.Logger` | Es el logger estándar de Go |
| `close()` | `Close() error` | Idempotente |

## SRE (`sre`)

| TypeScript | Go |
|---|---|
| `new CaerusClient({ apiKey, endpoint, tls, timeoutMs, logger })` | `sre.NewClient(sre.ClientOptions{APIKey, Endpoint, TLS, Timeout, Logger})` |
| `SharedResourceApi` | `sre.API` |
| `InMemoryCaerusClient` | `sre.InMemoryClient` (`sre.NewInMemoryClient(&sre.MockOptions{...})`) |
| `createUnitary` / `createMultiple` | `CreateUnitary` / `CreateMultiple` |
| `updateResource` / `deleteResource` | `UpdateResource` / `DeleteResource` |
| `unitary(key).take()` | `Unitary(key).Take(ctx, opts)` |
| `pooled(key).take()` / `.takeMany(n)` | `Pooled(key).Take(ctx, opts)` / `.TakeMany(ctx, n, opts)` |
| `confirm` / `release` / `extend(id, extraMs)` | `Confirm` / `Release` / `Extend(ctx, id, extra time.Duration)` |
| `getResourceHolder` / `getResource` | `GetResourceHolder` / `GetResource` |
| `getResourcesByGroup` / `listResourceHolders` | `GetResourcesByGroup` / `ListResourceHolders` |
| `TakeOptions.ttlSeconds` | `TakeOptions.TTL time.Duration` |
| `ResourceHolderStatus` (`'PENDING'`...) | `sre.ResourceHolderStatus` (`sre.StatusPending`...) |
| `sort: 'NEWEST_FIRST'` | `Sort: sre.NewestFirst` |
| `mock.failNext('take', error)` | `FailNext(sre.MockTake, err)` |
| `mock.advanceTime(seconds)` | `AdvanceTime(time.Duration)` |
| `mock.expire(id)` / `mock.snapshot()` | `Expire(id)` / `Snapshot()` |

## DLS (`dls`)

| TypeScript | Go |
|---|---|
| `new DlsClient({...})` | `dls.NewClient(dls.ClientOptions{...})` |
| `DlsApi` | `dls.API` |
| `InMemoryDlsClient` | `dls.InMemoryClient` (`dls.NewInMemoryClient()`) |
| `withTransaction(async tx => valor, opts)` | `WithTransaction(ctx, func(ctx, tx dls.Tx) error, opts)`; el resultado se guarda en una variable de afuera |
| `TransactionContext` | `dls.Tx` |
| `tx.signal` | El `ctx` que recibe el callback; `context.Cause(ctx)` dice por qué se canceló |
| `autoRenew: false` | `DisableAutoRenew: true` |
| `onTransactionLost(error)` | `OnTransactionLost func(error)` |
| `beginTransaction({ timeoutMs })` | `BeginTransaction(ctx, &dls.BeginTransactionOptions{Timeout})` |
| `acquireLock(ns, key, txId, mode, opts)` | `AcquireLock(ctx, ns, key, txID, mode, opts)` |
| `acquireLock` con `signal` | Cancelar el `ctx` |
| `onQueued` | `OnQueued func()` |
| `renewTransaction(txId, extraMs)` | `RenewTransaction(ctx, txID, extra time.Duration)` |
| `releaseLock` / `releaseTransactionLocks` | `ReleaseLock` / `ReleaseTransactionLocks` |
| `getLockStatus` / `getTransactionStatus` | `GetLockStatus` / `GetTransactionStatus` |
| `LockMode.EXCLUSIVE` / `SHARED_READ` | `dls.Exclusive` / `dls.SharedRead` |
| `LockStatus` | `dls.Acquired`, `dls.Denied`, `dls.Queued`, `dls.UnknownStatus` |
| `fencingToken` ausente | `FencingToken == 0` |
| `'Transaction context already closed'` | `dls.ErrTransactionClosed` |

## Errores

| TypeScript | Go |
|---|---|
| `CaerusError` (`code`, `reason`, `requestId`, `docUrl`) | `*caerus.Error` (`Code()`, `Reason()`, `RequestID()`, `DocURL()`) |
| `ResourceNotFoundError`, `ConflictError`, `OutOfStockError`... | `*sre.ResourceNotFoundError`, `*sre.ConflictError`, `*sre.OutOfStockError`... |
| `DlsError`, `DlsConflictError`, `DlsNotFoundError`... | `*dls.Error`, `*dls.ConflictError`, `*dls.NotFoundError`... |
| `DeadlockAbortedError`, `LockDeniedError`... | `*dls.DeadlockAbortedError`, `*dls.LockDeniedError`... |
| `CaerusSignatureError` | `*webhooks.SignatureError` |
| `CaerusWebhookExpiredError` | `*webhooks.ExpiredError` |
| `CaerusWebhookPayloadError` | `*webhooks.PayloadError` |

## Webhooks (`webhooks`)

| TypeScript | Go |
|---|---|
| `caerus.webhooks.constructEvent(payload, header, secret, tolerance)` | `webhooks.ConstructEvent(payload []byte, header, secret, &webhooks.ConstructEventOptions{Tolerance})` |
| Unión discriminada por `eventType` | `event.Data` es una interfaz; un struct por evento (`*webhooks.ResourceTakenData`...) |
| Evento desconocido | `*webhooks.UnknownEventData` con todos los campos |

## Diferencias de comportamiento con TS (a favor de parecerse al motor)

Son las mismas que decidió el SDK de Java:

- `InMemoryClient.ReleaseLock` de un lock o transacción que no existe responde OK, como el
  motor (TS lanza `DlsNotFoundError`).
- `InMemoryClient.AcquireLock` replica la idempotencia del motor: misma clave en la misma
  transacción devuelve el mismo lock; otra clave da `LockAlreadyHeldError`.
- `InMemoryClient.GetLockStatus` devuelve el fencing token real (TS devuelve `0`).
- Dentro de `WithTransaction`, una espera con su propio `ctx` también se corta si se pierde
  la transacción.
- Las renovaciones nunca se superponen: la siguiente no sale hasta que vuelve la anterior.
- La renovación automática lleva la cuenta del vencimiento y en cada renovación pide lo justo
  para volver a tener la vida entera por delante. TS y Java suman la vida entera cada media vida,
  así que el vencimiento avanza el doble de rápido que el reloj y, si el proceso se cae, los locks
  quedan tomados tanto como duró el trabajo. En Go, después de una caída los locks quedan tomados
  entre la mitad de la vida y la vida entera, más lo que haya tardado el `BeginTransaction`. Renueva cada media vida, también con
  vidas de menos de 1 s (TS y Java esperan como mínimo 1 s y una vida corta les vence antes de la
  primera renovación). Si el callback termina bien pero la transacción venció mientras corría,
  `WithTransaction` devuelve `*dls.TransactionNotActiveError` en vez de informar un éxito. El SDK mide con su propio reloj desde
  antes de pedir la transacción, no con el vencimiento que informa el motor: así un desfasaje entre
  relojes no puede hacerle creer que la transacción sigue viva cuando no. Cada renovación se corta al llegar al vencimiento: si no llega a tiempo, la
  transacción se da por perdida (`*dls.TransactionNotActiveError`) y se cancela el `ctx` del
  callback antes de que siga trabajando sin locks.
- La firma del webhook se calcula sobre los bytes crudos del cuerpo.
- Un modo de lock sin especificar en `GetTransactionStatus` se informa como `EXCLUSIVE`,
  igual que el motor.
- `dls.InMemoryClient` rechaza `AcquireLock` y `RenewTransaction` de una transacción vencida
  con `*dls.TransactionNotActiveError` (`TRANSACTION_NOT_ACTIVE`), como el motor, y
  `GetTransactionStatus` la informa como `MAX_LIFETIME_EXCEEDED`. TS y Java la siguen aceptando.
- `sre.InMemoryClient` copia la metadata en profundidad: modificar un mapa anidado de lo que
  devuelve no cambia lo que guarda (como el motor, que la manda serializada).

Y las propias de Go:

- `Unitary(key)` y `Pooled(key)` no fallan al armar el handle; una clave vacía se rechaza en
  `Take`.
- No hay `Webhooks` como campo del cliente: `ConstructEvent` es una función del paquete.
