# Verificación contra un motor real

Los tests de `go test ./...` corren contra un motor gRPC falso: son rápidos y no necesitan
infraestructura. Lo que no pueden probar es que el motor de verdad se comporte como el SDK
espera. Las dos veces que el SDK de TypeScript tuvo un defecto de contrato, los tests contra
el mock pasaban.

```bash
CAERUS_API_KEY=tu_clave SRE_TEMPLATE=butaca go test -tags live -count=1 -v ./internal/live/
```

Los tests en vivo llevan el build tag `live` y quedan fuera de `go test ./...`. Sin
`CAERUS_API_KEY` se saltean. El endpoint sale de `CAERUS_ENDPOINT` (por defecto el motor
hospedado) y TLS de `CAERUS_TLS` (por defecto prendido). Cada escenario imprime una línea
`[en vivo]` con lo que vio.

## DLS (`internal/live/dls_test.go`)

| Escenario | Plantilla | Qué demuestra |
|---|---|---|
| `TestSharedReadAndLockStatus` | `READ_WRITE` | Dos lectores entran a la vez, `GetLockStatus` los lista, un escritor no entra, soltar uno deja al otro |
| `TestDeniedLockInAFailNamespace` | `FAIL` | Un lock denegado llega como `*dls.LockDeniedError` con `LOCK_DENIED` |
| `TestTwoWayDeadlockWithKillPriority` | `QUEUE` | Una sola víctima, la más joven, con `DEADLOCK_DETECTED`, en menos de 10 s |
| `TestTriangularDeadlock` | `QUEUE` | Se rompe el ciclo sin matar a las tres |
| `TestDeadlockWithAlertStrategyAbortsNobody` | `ALERT` | No se aborta a nadie |
| `TestTransactionStatusWithALockInTheQueueAndOnQueued` | `QUEUE` | `GetTransactionStatus` responde con un lock en cola; `OnQueued` avisa una vez y antes de conceder |
| `TestAQueuedLockWithoutItsOwnTimeoutUsesTheClientDeadline` | `QUEUE` | Sin `Timeout` propio, la espera corta con el deadline del cliente |
| `TestCancellingTheContextCutsARealWaitInTheQueue` | `QUEUE` | Cancelar el `ctx` corta una espera real en la cola |
| `TestAClosedTransactionCannotTakeLocks` | `QUEUE` | Una transacción cerrada da `TRANSACTION_NOT_ACTIVE` |
| `TestWithTransactionRenewsALongJob` | `QUEUE` | Una transacción de 4 s sigue viva a los 7 s y su vencimiento avanza |
| `TestFencingTokensGrowAcrossHandoffs` | `QUEUE` | Los fencing tokens crecen de un dueño al siguiente |
| `TestAnIdempotencyKeyReplaysTheLockAndAnotherKeyIsAlreadyHeld` | `QUEUE` | Misma clave, mismo lock; otra clave, `LOCK_ALREADY_HELD_EXCLUSIVELY` |
| `TestAWrongAPIKeyIsAnAuthenticationErrorInTheDLS` | — | Una API Key que no existe da `*dls.AuthenticationError` |
| `TestAnEngineErrorCarriesARequestID` | — | El error trae el `RequestID` |

Los namespaces se eligen por variable de entorno:

| Variable | Default | `lock_type` | `conflict_resolution` | `deadlock_resolution_strategy` |
|---|---|---|---|---|
| `DLS_NS_FAIL` | `cuenta` | `EXCLUSIVE` | `FAIL` | `KILL_PRIORITY` |
| `DLS_NS_QUEUE` | `cuenta_cola` | `EXCLUSIVE` | `QUEUE` | `KILL_PRIORITY` |
| `DLS_NS_ALERT` | `cuenta_alerta` | `EXCLUSIVE` | `QUEUE` | `ALERT` |
| `DLS_NS_READ_WRITE` | `archivo` | `READ_WRITE` | `QUEUE` | `KILL_PRIORITY` |

Cada `AcquireLock` manda una clave de idempotencia, así que también sirven plantillas con
fencing token obligatorio. Una plantilla `READ_WRITE` con `QUEUE` sirve como `DLS_NS_QUEUE`:
los escenarios piden `EXCLUSIVE`.

## SRE (`internal/live/sre_test.go`)

Necesita `SRE_TEMPLATE` con el nombre de una plantilla de recursos del mismo entorno que la
key. Sin ella se saltea.

| Escenario | Qué demuestra |
|---|---|
| `TestTheWholeLifeOfAPooledResource` | Crear, tomar con idempotencia y metadata, repetir la clave, extender (redondeo a segundos), quedarse sin stock, confirmar, no poder borrar con holders activos, liberar, listar, buscar por grupo, actualizar stock y borrar |
| `TestAResourceThatDoesNotExistIsNotFound` | `*sre.ResourceNotFoundError` con `RequestID` |
| `TestAWrongAPIKeyIsAnAuthenticationErrorInTheSRE` | `*sre.AuthenticationError` |

`GetResourcesByGroup` lee de PostgreSQL, que se sincroniza por write-behind: el test
reintenta hasta 15 s antes de dar por perdido un recurso recién creado.

Ningún escenario crea un recurso y espera `NOT_FOUND` apenas lo borra: hoy el motor puede
"resucitar" un recurso borrado justo después de crearlo (los eventos de un mismo recurso
viajan por colas distintas de RabbitMQ y pueden llegar desordenados a PostgreSQL).

## No medir tiempos con otra corrida en marcha

Dos verificaciones contra el mismo motor se reparten el motor y los tiempos cambian. Los
umbrales de tiempo de estos tests asumen que corren solos.

## Última corrida

11/10/2026, contra `caerus.dev.ar.sdk.apps.disilab.ar:443`: los 17 escenarios en verde. Los
que necesitan plantillas `QUEUE` con `KILL_PRIORITY` y `READ_WRITE` corrieron en el entorno de
la demo del DLS (`task_processing`); el resto en `dev` (`butaca`, `cuenta`, `cuenta_alerta`).
