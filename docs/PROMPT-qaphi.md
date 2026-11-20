# Prompt: Qaphi — monitor de servicios ultraligero en Go

## Contexto y rol

Actúa como desarrollador sénior de Go. Vamos a construir **Qaphi**, una aplicación de monitorización de servicios (alternativa ultraligera a Gatus) con página de estado y web de administración, distribuida como **un único binario** sin dependencias externas.

El usuario es administrador de sistemas. La primera instalación será una NanoPi NEO (ARMv7 de 32 bits, 512 MB de RAM, microSD, Armbian), pero **la aplicación debe ser genérica**: nada específico de esa placa ni de ninguna distribución concreta. **Qaphi es solo para Linux.**

> Nombre definitivo: **Qaphi**. Binario `qaphi`, módulo y carpeta del proyecto `qaphi`, variables de entorno con prefijo `QAPHI_`.

---

## Goals (objetivos)

1. **Ligereza medible**
   - Binario final ≤ 20 MB (compilado con `-ldflags="-s -w"`).
   - Consumo de RAM (RSS) ≤ 30 MB con 20 checks activos cada 60 s.
2. **Portabilidad en Linux**: `CGO_ENABLED=0`; compilación cruzada desde un solo equipo para Linux amd64, Linux arm64 y Linux armv7 de 32 bits (`GOARCH=arm GOARM=7`). **Solo Linux: no se compila para Windows ni macOS.**
3. **Cero dependencias en el destino**: copiar el binario y ejecutarlo. Plantillas, CSS y JS embebidos con `embed`.
4. **Amable con el almacenamiento**: pocas escrituras a disco (pensado para microSD), sin perder incidencias ni configuración ante un corte de luz.
5. **Configuración 100 % desde la web**: sin ficheros YAML que editar a mano.
6. **Seguro por defecto**: una instalación nueva no expone nada hasta que el administrador lo decide.
7. **Instalación y actualización desatendidas**: se instala con un solo comando y, para actualizar, basta con ejecutar la nueva compilación, que se instala sola conservando los datos y vuelve a lanzar la aplicación si ya estaba en marcha.

## Fuera de alcance (no hacer)

- Windows y macOS: Qaphi solo se desarrolla y se distribuye para Linux.
- Varios usuarios o roles (hay un único usuario `admin`).
- Sondas distribuidas o varios nodos.
- Métricas del propio equipo (CPU, RAM, temperatura, disco) y comprobación de unidades systemd. Se podrían añadir más adelante como módulo opcional con *build tags*, pero no ahora.
- Monitorización de Docker vía socket.
- Frameworks JS (React, Vue…), Node.js en el build y librerías de gráficas.

---

## Requisitos funcionales

### Rutas

| Ruta | Contenido | Acceso |
|---|---|---|
| `/` | redirige a `/home` | — |
| `/setup` | configuración inicial de la contraseña | solo mientras no exista contraseña |
| `/login`, `/logout` | inicio y cierre de sesión | — |
| `/home` | página de estado | pública o con login, según ajuste |
| `/admin` | gestión de checks, avisos, tokens y ajustes | siempre con login |
| `/api/v1/...` | API JSON de estado | misma regla que `/home`; si es privada, con token de API |
| `/hb/<token>` | recepción de heartbeats | siempre abierta, autenticada por el token del propio heartbeat |

### Autenticación

- **Usuario único `admin`** con contraseña.
- **Primer inicio**: si no hay contraseña configurada, cualquier ruta redirige a `/setup`. Al arrancar, la app imprime en el log un **token de configuración de un solo uso**; `/setup` exige ese token junto con la contraseña y su confirmación. Una vez guardada, `/setup` deja de existir.
- Contraseña con hash **bcrypt** (`golang.org/x/crypto/bcrypt`).
- Sesión en cookie `HttpOnly`, `SameSite=Strict` y `Secure` cuando la petición llega por HTTPS; caducidad configurable.
- **Protección CSRF** en todos los formularios.
- **Limitación de intentos de login**: bloqueo temporal tras varios fallos.
- **Recuperación**: subcomando `qaphi reset-password`, solo desde consola. Nunca desde la web.

### Página de estado (`/home`)

- Ajuste en `/admin`: **«Página de estado pública»** (desactivado por defecto).
  - Desactivado → sin sesión, redirige a `/login`.
  - Activado → visible sin login, pero **sin mostrar destinos** (URLs, IPs, puertos). Solo nombre, grupo, estado, uptime, tiempo de respuesta y minigráfica.
  - Con sesión iniciada se muestran también los destinos.
- Servicios agrupados (Red, Servicios, Externos…), resumen global («3/4 (1 caído)»), uptime 24 h / 7 d / 30 d, últimas incidencias.
- **Minigráfica de tiempo de respuesta en SVG generado en el servidor**.
- Modo oscuro (automático con `prefers-color-scheme`) y diseño apto para móvil.

### Tipos de check

| Tipo | Parámetros | Resultado |
|---|---|---|
| **HTTP(S)** | URL, método, código esperado, tiempo máximo, palabra clave opcional en el cuerpo (debe contener / no debe contener), ignorar TLS inválido (opcional) | ok/fallo, código, tiempo de respuesta |
| **Caducidad TLS** | en checks HTTPS: aviso si quedan menos de N días | estado de aviso diferenciado |
| **TCP** | host, puerto, tiempo máximo | ok/fallo, tiempo de conexión |
| **Ping (ICMP)** | host, nº de paquetes, tiempo máximo | latencia, % de pérdida. Si el sistema no da permisos (sockets sin privilegio o `CAP_NET_RAW`), el check queda en un estado «sin permisos» visible en la interfaz, con la explicación. |
| **DNS** | nombre, tipo (A, AAAA, CNAME, MX, TXT), servidor DNS a usar, valor esperado opcional | ok/fallo, tiempo de resolución |
| **Heartbeat** | periodo esperado y margen | el check falla si no recibe una petición a `/hb/<token>` dentro del plazo |

Parámetros comunes a todos: nombre, grupo, intervalo, **número de fallos consecutivos para marcar caído** (por defecto 3), activo/pausado, dependencia de otro check y canales de aviso.

### Lógica de estado

- **Confirmación**: un check solo pasa a caído tras N fallos consecutivos, y vuelve a up al primer éxito.
- **Dependencias**: si el check padre está caído, los hijos se muestran como «inaccesible por dependencia» y **no generan avisos**.
- **Mantenimiento**: pausar un check, o todos, sin borrar datos; mientras dura no genera incidencias ni avisos.
- **Incidencias**: cada transición up → caído → up se registra con inicio, fin, duración y último error.

### Avisos

- Canales: **ntfy**, **Telegram**, **webhook genérico** (POST JSON con un formato documentado) y **correo SMTP** (`net/smtp`).
- Se configuran en `/admin` con un botón **«Enviar prueba»**.
- Cada check elige sus canales. Se avisa al caer y al recuperarse, sin repetir avisos mientras sigue caído.

### Administración (`/admin`)

- CRUD de checks y de canales de aviso, con validación en el servidor y mensajes de error claros.
- Ajustes: página pública sí/no, retenciones, intervalo de volcado, caducidad de sesión.
- Tokens de API: crear, ver el último uso y revocar. Se guardan con hash; el valor se muestra una sola vez.
- **Exportar e importar** la configuración (checks, canales y ajustes) en JSON. El historial no se incluye.

### Línea de comandos

```
qaphi serve            # arranca el servidor (por defecto)
qaphi status           # resumen de una línea: "4/4 (todos up)" o "3/4 (1 caído)"
qaphi status --json    # el mismo resumen en JSON
qaphi reset-password   # restablece la contraseña de admin (interactivo)
qaphi install          # instala o actualiza (desatendido)
qaphi version
```

`qaphi status` consulta al proceso en marcha a través de un **socket Unix local** dentro del directorio de datos. Si no hay proceso, imprime «no responde» y sale con código distinto de 0. Está pensado para usarse en el MOTD, en scripts o con Nagios.

### Instancia única

- Solo puede correr **una instancia de qaphi por sistema**.
- SQLite en `locking_mode=EXCLUSIVE`: protege el directorio de datos (y evita el fichero `-shm`).
- Bloqueo global que el sistema libera solo si el proceso muere (socket Unix abstracto `@qaphi`, sin ficheros ni rutas fijas). Solo `serve` lo toma; si ya hay una instancia, sale con un error claro y código distinto de 0.
- Consecuencia: `qaphi reset-password` necesita el servicio detenido; el propio comando puede detenerlo y volver a arrancarlo.

### Instalación y actualización

- **Instalación desatendida con un solo comando.**
- **Actualización**: basta con ejecutar la nueva compilación; se instala sola, conserva datos y configuración, y vuelve a lanzar la aplicación si ya estaba en marcha. Las migraciones de base de datos se aplican solas al arrancar.
- Las opciones de arranque indicadas al instalar se conservan en las actualizaciones.
- Detalles pendientes de decidir: forma exacta del comando (propuesta: `qaphi install`), gestor de servicios (propuesta: solo systemd), vuelta atrás automática si la versión nueva no arranca (propuesta: sí), usuario del servicio (propuesta: usuario fijo `qaphi`), `qaphi uninstall` (propuesta: conserva los datos salvo `--purge`) y fase de implementación (propuesta: fase 1).

---

## Requisitos no funcionales

### Stack y dependencias

- Go (última versión estable; hoy 1.27, fijada en `go.mod`). **Librería estándar siempre que se pueda**: `net/http` (con el router de patrones de Go 1.22+), `html/template`, `log/slog`, `embed`, `database/sql`.
- Únicas dependencias permitidas sin consultar:
  - `modernc.org/sqlite` (SQLite en Go puro, sin CGO)
  - `golang.org/x/crypto` (bcrypt)
  - `golang.org/x/net` (ICMP)
- JS: opcional, un único fichero pequeño embebido (htmx o JS propio mínimo). La interfaz debe funcionar con JS desactivado, salvo detalles cosméticos.
- **Cualquier otra dependencia requiere consultarlo antes** y justificar su peso.

### Almacenamiento (SQLite)

- Un único fichero `qaphi.db` en el directorio de datos.
- Modo **WAL** con `locking_mode=EXCLUSIVE` y `synchronous=FULL`, **una sola conexión** (`SetMaxOpenConns(1)`) y `cache_size` limitado (2 MiB).
- **Migraciones versionadas** y embebidas, aplicadas automáticamente al arrancar.
- **Escrituras en lote**: los resultados se acumulan en memoria y se vuelcan en una sola transacción cada X minutos (configurable; 5 por defecto).
- **Incidencias, cambios de configuración y ajustes se escriben al momento.**
- Retención: resultados en bruto 48 h (configurable); agregados por hora (nº de checks, nº de fallos, tiempo medio, mínimo y máximo) durante 90 días (configurable); limpieza periódica.
- Modo opcional **«solo agregados»** (no guarda resultados en bruto) para almacenamiento delicado.
- Al recibir SIGTERM/SIGINT: **parada ordenada** que vuelca el buffer antes de salir.
- Acceso a datos detrás de una **interfaz `Store`**, para poder cambiar de motor sin tocar el resto.

### Configuración de arranque

Flags con su equivalente en variable de entorno. No hay rutas fijas.

| Flag | Variable | Por defecto |
|---|---|---|
| `--listen` | `QAPHI_LISTEN` | `:8091` |
| `--data-dir` | `QAPHI_DATA_DIR` | `./data` |
| `--log-level` | `QAPHI_LOG_LEVEL` | `info` |
| `--log-format` | `QAPHI_LOG_FORMAT` | `text` (o `json`) |
| `--flush-interval` | `QAPHI_FLUSH_INTERVAL` | `5m` (entre 10 s y 1 h) |

### Compatibilidad con 32 bits (armv7)

- Contadores y valores compartidos de 64 bits con los tipos `atomic.Int64` / `atomic.Uint64`, que se alinean solos. Nunca `atomic.AddInt64(&campo, …)` sobre campos sueltos de un struct: en ARM de 32 bits pueden quedar mal alineados y provocar un pánico.
- No asumir que `int` es de 64 bits: usar `int64` explícito para marcas de tiempo, duraciones, contadores acumulados y tamaños.
- Comprobar en cada fase que el proyecto compila para `linux/arm` (`GOARM=7`).

### Planificador y checks

- Planificador con *jitter* inicial, para no lanzar todos los checks a la vez.
- Concurrencia máxima configurable.
- Timeout en todos los checks: ninguno puede bloquear el planificador.
- Un check que entra en pánico no tumba la app: se recupera, se registra en el log y se marca como fallo.

### Seguridad

- Cabeceras: CSP estricta, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`.
- Escapado correcto en plantillas (`html/template`), sin HTML generado concatenando cadenas.
- Secretos (tokens de Telegram, contraseña SMTP…) nunca en los logs.
- Tokens de API y de heartbeat generados con `crypto/rand`.

### Logs

`log/slog` a stdout (en texto; JSON opcional), compatible con journald.

### Estilo de código

- Identificadores en inglés; **comentarios, mensajes de la interfaz y documentación en castellano**.
- Textos de la interfaz centralizados, para facilitar una traducción futura.
- Código formateado con `gofmt` y sin avisos de `go vet`.

---

## Arquitectura propuesta

```
qaphi/
├── cmd/qaphi/main.go          # subcomandos: serve, status, reset-password, install, version
├── internal/
│   ├── app/                   # ensamblado y ciclo de vida (arranque, parada ordenada)
│   ├── config/                # flags + variables de entorno
│   ├── logging/               # logger slog
│   ├── module/                # registro genérico de módulos y declaración de campos
│   ├── msg/                   # textos de la interfaz centralizados
│   ├── version/               # versión inyectada al compilar
│   ├── store/                 # interfaz Store
│   │   └── sqlite/            # implementación + migraciones embebidas
│   ├── checks/                # interfaz Checker + http, tcp, icmp, dns, heartbeat
│   ├── scheduler/             # planificación, confirmación de fallos, dependencias
│   ├── state/                 # estado actual en memoria, buffer, agregados
│   ├── notify/                # ntfy, telegram, webhook, smtp
│   ├── auth/                  # setup, login, sesiones, CSRF, rate limit
│   └── web/                   # handlers, plantillas y estáticos embebidos, SVG
├── deploy/
│   └── qaphi.service          # unit de systemd de ejemplo (usuario sin privilegios)
├── Makefile                   # build, test, cross-compile
└── README.md
```

La estructura puede ajustarse si hay una razón clara, explicándola.

---

## Procedimiento de trabajo

1. **Trabaja por fases.** Al terminar cada fase:
   - el proyecto compila (`go build ./...`) y pasan `go vet ./...` y `go test ./...`;
   - **detente**, resume lo hecho, los criterios cumplidos y cualquier decisión tomada, y **espera confirmación antes de empezar la siguiente fase**.
2. **No añadas funcionalidades fuera de la fase en curso** ni dependencias fuera de la lista permitida sin preguntar.
3. Ante una ambigüedad que afecte al diseño, **pregunta** en lugar de suponer. En detalles menores, decide, aplica lo razonable e indícalo en el resumen.
4. Tests:
   - checks con `httptest` y listeners locales;
   - `Store` con base de datos temporal;
   - lógica de estado (confirmación, dependencias, incidencias) con tests de tabla.
5. Al final de cada fase, indica el tamaño del binario para `linux/arm` (`GOARM=7`).

### Fases

**Fase 0 — Esqueleto**
- Módulo, estructura de carpetas, configuración (flags y variables de entorno), logging.
- `Store` + SQLite (WAL, migraciones), subcomandos `serve` y `version`, parada ordenada.
- Makefile con compilación cruzada para las tres arquitecturas Linux objetivo.
- ✅ Criterio: `qaphi serve` arranca, crea `qaphi.db`, responde en `/` y se detiene limpiamente con Ctrl+C, **también ejecutando el binario `linux/arm` en un equipo ARMv7 de 32 bits real** (no solo compilándolo), para validar SQLite en esa arquitectura.

**Fase 1 — Autenticación y núcleo**
- `/setup` con token de un solo uso, `/login`, `/logout`, sesiones, CSRF, rate limit, `reset-password`.
- CRUD de checks HTTP(S) y TCP en `/admin`.
- Planificador, buffer en memoria con volcado en lote, estado actual.
- `/home` básico con estado y tiempo de respuesta.
- ✅ Criterio: desde cero, configurar la contraseña, crear 3 checks y verlos actualizarse en `/home`. Tras reiniciar, los datos persisten (salvo como mucho el último intervalo de volcado).

**Fase 2 — Checks completos y estado**
- Palabra clave, caducidad TLS, ICMP (con estado «sin permisos»), DNS, heartbeat.
- Confirmación de N fallos, incidencias, agregados por hora, uptime 24 h / 7 d / 30 d, retención y limpieza, modo «solo agregados».
- Minigráficas SVG, grupos, modo oscuro, ajuste de página pública con ocultación de destinos.
- ✅ Criterio: provocar la caída de un servicio y que se registre una incidencia con su duración. El modo público no muestra ningún destino.

**Fase 3 — Avisos e integración**
- Canales ntfy, Telegram, webhook y SMTP con «Enviar prueba».
- Dependencias entre checks y modo mantenimiento.
- API JSON, tokens de API, `qaphi status` (socket local) y exportar/importar configuración.
- ✅ Criterio: un aviso al caer y otro al recuperarse, sin repeticiones. Con el padre caído, los hijos no avisan. `qaphi status` devuelve el resumen correcto.

**Fase 4 — Pulido y entrega**
- Revisión de seguridad (cabeceras, CSRF, escapado, secretos fuera de los logs).
- **Medición real**: tamaño del binario y RSS con 20 checks a 60 s durante 30 min. Si se superan los objetivos, optimizar (`GOMEMLIMIT`, `cache_size`, asignaciones).
- `README.md`: instalación, flags, primer inicio, `qaphi status` en el MOTD, unit de systemd, formato del webhook, permisos ICMP en Linux (`ping_group_range` o `CAP_NET_RAW`).
- `deploy/qaphi.service` con usuario sin privilegios, `Restart=on-failure` y `AmbientCapabilities=CAP_NET_RAW` comentado para ICMP.
- ✅ Criterio: todos los goals cumplidos y documentados con las cifras medidas.

---

## Decisiones tomadas por defecto (revisables)

- **Plataformas**: solo Linux (amd64, arm64 y armv7 de 32 bits). Sin Windows ni macOS.
- **Instancia única** por sistema, con bloqueo exclusivo de SQLite.
- **Métricas del equipo y systemd**: fuera de esta versión.
- **Puerto por defecto**: 8091.
- **Página de estado pública**: desactivada por defecto.
