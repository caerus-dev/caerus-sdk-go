# Errores

Todo error que devuelve el SDK se puede mirar como `*caerus.Error`:

```go
var caerusErr *caerus.Error
if errors.As(err, &caerusErr) {
	log.Printf("Caerus %s (%s) request %s", caerusErr.Code(), caerusErr.Reason(), caerusErr.RequestID())
}
```

| Método | Qué trae |
|---|---|
| `Code()` | `caerus.CodeResourceNotFound`, `CodeConflict`, `CodeValidation`, `CodeAuthentication`, `CodeTimeout` o `CodeUnknown` |
| `Reason()` | El código tal cual lo mandó el motor (`OUT_OF_STOCK`...), `""` si no mandó ninguno |
| `RequestID()` | El `x-request-id` de la llamada |
| `DocURL()` | Este documento |
| `Unwrap()` | La causa (el error de gRPC, el de JSON...) |

Además, cada caso tiene su propio tipo, y los tipos respetan la jerarquía del SDK de
TypeScript: un `*sre.OutOfStockError` también es un `*sre.ConflictError` y un
`*caerus.Error` para `errors.As`.

```go
var conflict *sre.ConflictError
if errors.As(err, &conflict) {
	return "no hay lugar"
}
```

Los errores de configuración (una API Key vacía, un `Timeout` negativo) salen de
`NewClient`, no en la primera llamada, y son `error` comunes. Usar un `dls.Tx` después de
que terminó `WithTransaction` devuelve `dls.ErrTransactionClosed`.

## El mapeo del SRE

Refleja lo que hace el servidor en `GrpcGlobalExceptionHandler`, no una convención general
de gRPC.

| Estado gRPC | Tipo | `Code()` | Cuándo |
|---|---|---|---|
| `NOT_FOUND` | `*sre.ResourceNotFoundError` | `RESOURCE_NOT_FOUND` | El recurso, la plantilla o el holder no existen |
| `FAILED_PRECONDITION` | `*sre.ConflictError` | `CONFLICT` | El estado no permite la operación |
| `INVALID_ARGUMENT` | `*sre.ValidationError` | `VALIDATION` | El pedido está mal formado |
| `UNAUTHENTICATED` | `*sre.AuthenticationError` | `AUTHENTICATION` | API Key ausente, desconocida o revocada |
| `DEADLINE_EXCEEDED` | `*sre.TimeoutError` | `TIMEOUT` | La llamada pasó su deadline |
| cualquier otro | `*caerus.Error` | `UNKNOWN` | Incluye `INTERNAL` y una llamada cancelada por el `ctx` |

## `ConflictError` junta varias cosas, y se distinguen

| Tipo | `Reason()` | Cuándo |
|---|---|---|
| `*sre.OutOfStockError` | `OUT_OF_STOCK` | No quedan unidades libres |
| `*sre.HolderNotActiveError` | `HOLDER_NOT_ACTIVE` | El holder está `RELEASED`, `CONFIRMED` o `EXPIRED` |
| `*sre.ResourceHasActiveHoldsError` | `RESOURCE_HAS_ACTIVE_HOLDS` | No se puede borrar: alguien lo tiene tomado |
| `*sre.ResourceHasQueuedRequestsError` | `RESOURCE_HAS_QUEUED_REQUESTS` | No se puede borrar: hay gente en la cola |

Los cuatro siguen siendo `*sre.ConflictError`. El mensaje está escrito para personas y
puede cambiar; `Reason()` es contrato.

## Errores del DLS

Todos son `*dls.Error`, que es `*caerus.Error`. Primero decide el `Reason()`, después el
estado gRPC.

| Tipo | `Reason()` | Estado gRPC sin `Reason()` | Padre | Cuándo |
|---|---|---|---|---|
| `*dls.DeadlockAbortedError` | `DEADLOCK_DETECTED` | — | `ConflictError` | El detector eligió esta transacción como víctima |
| `*dls.LockDeniedError` | `LOCK_DENIED` | — | `ConflictError` | El lock lo tiene otro y la plantilla no encola |
| `*dls.LockAlreadyHeldError` | `LOCK_ALREADY_HELD_EXCLUSIVELY` | `ALREADY_EXISTS` | `ConflictError` | La transacción ya tiene ese lock |
| `*dls.TransactionNotActiveError` | `TRANSACTION_NOT_ACTIVE` | — | `ConflictError` | La transacción terminó o venció |
| `*dls.LockModeMismatchError` | `LOCK_MODE_MISMATCH` | — | `ValidationError` | El modo no corresponde a la plantilla |
| `*dls.LockAcquisitionCancelledError` | `LOCK_ACQUISITION_CANCELLED` | `CANCELLED` | `Error` | El motor canceló la espera |
| `*dls.ConflictError` | — | `ABORTED`, `FAILED_PRECONDITION` | `Error` | Otro conflicto |
| `*dls.NotFoundError` | — | `NOT_FOUND` | `Error` | La transacción o el lock no existen |
| `*dls.ValidationError` | — | `INVALID_ARGUMENT` | `Error` | Pedido mal formado |
| `*dls.AuthenticationError` | — | `UNAUTHENTICATED` | `Error` | API Key inválida |
| `*dls.TimeoutError` | — | `DEADLINE_EXCEEDED` | `Error` | Se pasó el deadline |
| `*dls.Error` | — | otro | `caerus.Error` | `UNKNOWN` |

El motor manda un `DENIED` como respuesta exitosa del stream, no como error gRPC. El SDK no
lo devuelve como valor: `AcquireLock` devuelve `*dls.LockDeniedError` con `Reason()`
`LOCK_DENIED`.

Un `AcquireLock` sin `Timeout` propio usa el deadline del cliente (10 s por defecto),
también dentro de `WithTransaction`. Un lock en cola más tiempo que eso termina en
`*dls.TimeoutError`.

Cancelar el `ctx` de un `AcquireLock` en espera corta el stream y devuelve un `*dls.Error`
`"AcquireLock aborted by user"` cuya causa es la del `ctx`: `errors.Is(err,
context.Canceled)` da `true`. Si se cortó porque la transacción se perdió, la causa es ese
error (por ejemplo un `*dls.DeadlockAbortedError`).

## `RequestID` y soporte

Cada llamada manda un `x-request-id` (`req_` + UUID) y el motor lo devuelve en los trailers.
Todo error lo trae en `RequestID()` y lo agrega al mensaje:
`... (Request ID: req_...). Contacta soporte en Discord con este ID.`

Con ese ID el equipo encuentra la traza exacta en los logs del motor sin que tengas que
compartir claves ni datos.

## `TimeoutError` no significa que no pasó nada

Significa que la llamada pasó su deadline. Si el servidor llegó a hacer el trabajo, no se
sabe. Reintentar con la misma clave de idempotencia devuelve el holder que ya existía si
sigue `PENDING` o `QUEUED`. Si ya terminó (`CONFIRMED`, `RELEASED` o `EXPIRED`), el motor
lo devuelve igual pero el SDK devuelve `*sre.ConflictError`: nunca te entrega un holder que
no podés usar. Para ver en qué quedó, usá `GetResourceHolder`.

En el DLS pasa lo mismo con `AcquireLock`: repetir la misma clave de idempotencia en la
misma transacción devuelve el mismo lock (mismo `LockID` y fencing token); pedir el mismo
lock con otra clave da `*dls.LockAlreadyHeldError`.

## El SDK no reintenta

Ninguna operación se reintenta sola, ni siquiera las lecturas. En un sistema de reservas los
reintentos silenciosos son exactamente cómo se entrega la misma butaca dos veces.
