# Publicar una versión

En Go no hay un registro aparte: una versión es un tag de git con forma `vX.Y.Z` en el
repositorio público. `go get` la baja de GitHub a través de `proxy.golang.org`, y
`pkg.go.dev` la muestra.

**Es irreversible.** El proxy guarda cada versión para siempre, aunque después se borre el
tag. Lo único que se puede hacer con una versión mala es marcarla como retirada con una
directiva `retract` en una versión nueva. Por eso el tag lo crea Thomas.

## Antes del tag

1. `main` con el CI en verde (`tests.yml`: tests con `-race` en Go 1.26 y 1.27, staticcheck,
   govulncheck y código generado al día).
2. La verificación en vivo en verde ([verificacion-en-vivo.md](verificacion-en-vivo.md)).
3. `caerus.Version` en `caerus.go` igual a la versión que se va a publicar, en un commit
   propio.

## Publicar

```bash
git tag v0.1.0
git push origin v0.1.0
```

Después, pedirle la versión al proxy para que quede registrada y aparezca en `pkg.go.dev`:

```bash
GOPROXY=proxy.golang.org go list -m github.com/caerus-dev/caerus-sdk-go@v0.1.0
```

## Probar lo publicado

En un módulo vacío, contra el motor real:

```bash
go mod init prueba
go get github.com/caerus-dev/caerus-sdk-go@v0.1.0
```

## Versiones

Mientras el SDK esté en `v0.x`, una versión menor puede romper la API. Desde `v1.0.0` rige
semver: un cambio que rompe obliga a `v2`, y en Go eso además cambia la ruta del módulo
(`github.com/caerus-dev/caerus-sdk-go/v2`).
