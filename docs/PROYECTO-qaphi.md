# Proyecto Qaphi — instrucciones

## Qué es
Qaphi es un monitor de servicios ultraligero en Go (alternativa a Gatus/Uptime Kuma) con página de estado y web de administración. Se distribuye como **un único binario** sin dependencias. **Solo Linux**, pero **genérico** dentro de Linux: debe funcionar en cualquier distribución y en todas las arquitecturas objetivo (la primera instalación es una NanoPi NEO ARMv7 de 32 bits con 512 MB y microSD, pero nada puede ser específico de ella).

Actúa como desarrollador sénior de Go. Responde en castellano. Identificadores en inglés; comentarios del código y documentación en castellano; **logs y salida de consola en inglés**; interfaz web traducida (castellano, inglés, alemán y francés).

## Goals
- Binario ≤ 20 MB (`-ldflags="-s -w"`) y RAM ≤ 30 MB con 20 checks cada 60 s. **Lo que debe ser ligero es el servidor**; el navegador puede asumir más carga (JavaScript, gráficas).
- `CGO_ENABLED=0`, compilación cruzada para Linux amd64, arm64, armv7 (`GOARCH=arm GOARM=7`) y armv6 (`GOARM=6`, Raspberry Pi Zero/1). **Solo Linux: sin Windows ni macOS.**
- Plantillas, CSS y JS embebidos (`embed`); nada que instalar en el destino.
- Pocas escrituras a disco, sin perder incidencias ni configuración ante un corte de luz.
- Toda la configuración desde la web, sin ficheros que editar a mano.
- Seguro por defecto: nada expuesto hasta que el admin lo decide.
- **Modular**: añadir, quitar o modificar elementos y funciones sin tocar el resto.
- **Instalación y actualización desatendidas, con un solo comando**, conservando los datos.

## Fuera de alcance
Windows y macOS, varios usuarios o roles (**un único usuario y contraseña, decisión firme**), sondas distribuidas, métricas del propio equipo y comprobación de unidades systemd, Docker vía socket, Node.js/npm en el build, recursos cargados desde internet (CDN), servir Qaphi en una subruta detrás de un proxy (se accede por IP:puerto o por subdominio) y certificados de Let's Encrypt.

## Stack
- Go, última versión estable (hoy 1.27, fijada en `go.mod`). Módulo `github.com/future-chocolate6122/qaphi`. Licencia MIT (titular: Future-Chocolate6122).
- Librería estándar siempre que se pueda (`net/http` con patrones de Go 1.22+, `html/template`, `log/slog`, `database/sql`).
- Dependencias Go permitidas: `modernc.org/sqlite`, `golang.org/x/crypto` (bcrypt) y `golang.org/x/net` (ICMP). **Cualquier otra, consultar antes.**
- **JavaScript:** se ejecuta en el navegador, no en el servidor. Preferiblemente código propio; se admiten librerías pequeñas con licencia MIT o similar, incluidas en el binario y **consultándolo antes**. Sin Node.js ni npm para compilar y sin CDN. Login, setup y formularios de `/admin` funcionan **sin** JavaScript; JavaScript se usa en las páginas de estado de los servicios (actualización en vivo y diseño visual).

## Modularidad
1. **Código: interfaces y registro.** `Component` (visuales), `Checker` (tipos de check), `Notifier` (avisos) y `Theme` (temas). Cada módulo es un fichero que se registra solo y **declara sus campos y opciones**; los formularios de `/admin` se generan a partir de esa declaración. Los componentes visuales reciben un modelo de datos común y nunca acceden al almacenamiento. Mecanismo común en `internal/module` (registro genérico + declaración y validación de campos); cada módulo registra sus textos en los cuatro idiomas desde su propio fichero.
2. **Ejecución: sección "Diseño" en `/admin`.** Activar o desactivar componentes, ordenarlos en `/home` y ajustar sus opciones; se guarda en la base de datos y entra en exportar e importar.
3. **Temas:** un tema es un conjunto de variables CSS (colores de estado, fondos, radios, espaciados) declarado en un fichero Go que se registra solo; Qaphi genera con ellos una hoja de estilos cacheable. Temas iniciales: Automático (sigue al sistema), Claro y Oscuro. Los nombres de las variables CSS son estables (son la API de los temas).
4. **Compilación (futuro):** build tags para excluir módulos del binario.

## Funcionalidad
**Rutas:** `/` → `/home` · `/setup` (solo mientras no hay contraseña) · `/login` · `/logout` · `/home` (pública o con login, según ajuste) · `/admin` (siempre con login) · `/api/v1` (misma regla que `/home`; si es privada, con token) · `/hb/<token>` (heartbeats).

**Autenticación:** usuario único `admin`. En el primer inicio, `/setup` pide la contraseña junto con un **token de un solo uso impreso en el log**. Hash bcrypt, cookie `HttpOnly` + `SameSite=Strict` (+ `Secure` con HTTPS), CSRF, límite de intentos. La contraseña solo se recupera desde consola: `qaphi reset-password`. Por el bloqueo exclusivo, el propio comando detiene el servicio, hace el cambio, deja los ficheros de la base de datos con el propietario del servicio y lo vuelve a arrancar.

**Ajustes globales en `/admin`** (para toda la instalación):
- **Idioma:** castellano, inglés, alemán o francés. También se elige en `/setup` (preseleccionado según el idioma del navegador). Se traduce **toda la interfaz web**, incluidos los textos de los checks; no se traducen los logs ni la salida técnica de consola. Los mensajes de resultado se guardan como código + datos y se traducen al mostrarlos. Formatos por idioma (coma decimal, «hace 5 s»).
- **Tema:** Automático, Claro u Oscuro (ampliable). Se aplica a toda la interfaz, incluidos `/setup` y `/login`.
- **Zona horaria:** desplegable; por defecto la del sistema. La base de datos de zonas horarias va incluida en el binario.
- **Red:** dirección de escucha (todas las interfaces, solo local o una IP), **un único puerto** (por defecto **9077**, solo ≥ 1024) y protocolo **HTTP o HTTPS** (certificado autofirmado generado por Qaphi y guardado en la base de datos, fuera de la exportación). El cambio de dirección, puerto o protocolo se aplica sin reiniciar y **hay que confirmarlo en 2 minutos desde la dirección nueva; si no, se restaura la anterior**. El flag o la variable de entorno prevalecen como recurso de emergencia y la web los muestra como fijados al arrancar.

**Página pública:** desactivada por defecto. En modo público oculta los destinos (URLs, IPs, puertos).

**Checks:** HTTP(S) (código, tiempo máximo, palabra clave, caducidad TLS) · TCP · ping ICMP (estado «sin permisos» si el sistema no los da) · DNS (tipo, servidor, valor esperado) · heartbeat. Comunes a todos: grupo, intervalo, N fallos para marcar caído (3 por defecto), pausa, dependencia de otro check y canales de aviso.

**Lógica de estado:** confirmación de N fallos · dependencias (los hijos de un padre caído no avisan) · mantenimiento · incidencias con inicio, fin y duración · uptime 24 h / 7 d / 30 d · estado «degradado» por umbral de lentitud.

**Avisos:** ntfy, Telegram, webhook (JSON documentado) y SMTP, con «Enviar prueba». Aviso al caer y al recuperarse, sin repeticiones.

**Admin:** CRUD de checks y canales, sección Diseño, ajustes, tokens de API (guardados con hash, visibles una sola vez), exportar e importar en JSON.

**CLI:** `qaphi serve | status [--json] | reset-password | install | uninstall | version`. `status` imprime «4/4 (all up)» o «3/4 (1 down)», consultando al proceso en marcha por un socket Unix local en el directorio de datos.

**Instancia única (autocontenida, la garantiza el propio binario):** solo puede correr una instancia de qaphi por sistema. Dos capas: SQLite en `locking_mode=EXCLUSIVE` (protege el directorio de datos y evita el fichero `-shm`) y un bloqueo global que el sistema libera solo si el proceso muere (socket Unix abstracto `@qaphi`, sin ficheros ni rutas fijas). Solo `serve` toma el bloqueo; si ya hay una instancia, sale con error claro y código ≠ 0.

**Instalación y actualización:** desatendida, con un solo comando: `sudo ./qaphi install [opciones]`, que sirve para instalar y para actualizar. Solo systemd. En el destino no hace falta preparar nada: basta Linux con systemd, root y `useradd` (de serie). Se recomienda NTP (Armbian lo trae).
- *Instalación nueva:* comprueba root y systemd (si falta algo, explica qué y sale sin cambiar nada) → crea el usuario de sistema `qaphi` (sin contraseña, sin shell, sin home) → copia el binario a `/usr/local/bin/qaphi` (temporal + rename atómico) → crea `/var/lib/qaphi` (0700, de `qaphi`) → genera `/etc/systemd/system/qaphi.service` → `systemctl enable --now` → espera a que responda y muestra la URL y el token de configuración inicial.
- *Actualización (el mismo comando con el binario nuevo):* detecta la instalación y anota si el servicio estaba activo y habilitado → compara versiones semver (una versión anterior exige `--force`; las compilaciones sin etiqueta, «dev», se instalan con aviso) → para el servicio de forma ordenada → copia `qaphi.db` y guarda el binario anterior como `qaphi.prev` → sustituye el binario y regenera la unit → arranca solo si estaba en marcha → comprueba que responde; si no, **vuelta atrás automática** (binario y base de datos) y lo vuelve a arrancar.
- *Opciones de arranque* (`--listen`, etc.): se guardan en `/etc/qaphi/qaphi.env`, generado por el instalador (nadie lo edita). Con la configuración de red en la web, `--listen` queda como recurso de emergencia.
- *Unit generada:* usuario `qaphi`, endurecida (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, escritura solo en `/var/lib/qaphi`), `Restart=on-failure`, `AmbientCapabilities=CAP_NET_RAW` y `CapabilityBoundingSet=CAP_NET_RAW` **activos** (ping ICMP sin configurar nada), **sin** `CAP_NET_BIND_SERVICE` (por eso los puertos van desde 1024), `After=network-online.target time-sync.target`.
- *`qaphi uninstall`:* para y quita servicio, unit, binario y usuario; conserva los datos salvo `--purge`.
- *`install.sh` (fase de pulido, opcional):* detecta la arquitectura, descarga el binario de GitHub Releases y ejecuta `qaphi install` (`curl … | sudo sh`).

**Reloj (equipos sin RTC):** mientras la hora del sistema sea anterior a la fecha de compilación del binario, Qaphi no registra resultados ni incidencias y lo avisa en el log; empieza en cuanto NTP sincroniza.

## Diseño visual (páginas de estado)
El servidor entrega los datos en JSON y **el navegador dibuja** con JavaScript; objetivo: un acabado tipo Uptime Kuma o mejor. `/home` se actualiza en vivo pidiendo un JSON cada pocos segundos.
- **Barras:** altura proporcional al tiempo de respuesta · barras agregadas con proporción de fallos apilada · verde, ámbar (lento) y rojo · mantenimiento con rayado diagonal · marcadores de incidencia · barras antiguas más tenues · la última «late» (respetando `prefers-reduced-motion`) · tooltips.
- **Página:** banner de estado global · favicon que cambia de color según el estado y contador en el título · anillo de uptime · selector de rango 1 h / 24 h / 7 d / 30 d · mapa de calor de 90 días en el detalle.
- Temas (claro, oscuro, automático) y diseño apto para móvil.
- Cada elemento es un `Component` del sistema modular.

## Almacenamiento (SQLite)
`qaphi.db` en modo WAL con `locking_mode=EXCLUSIVE` y `synchronous=FULL`, una sola conexión, `cache_size` de 2 MiB, migraciones embebidas. Resultados en un buffer en memoria con volcado en lote cada 5 min (configurable); incidencias y configuración, al momento. Resultados en bruto 48 h y agregados por hora 90 días (ambos configurables); modo opcional «solo agregados». Parada ordenada que vuelca el buffer. Acceso a datos tras una interfaz `Store`.

## Compatibilidad con 32 bits (armv7)
Valores de 64 bits compartidos con `atomic.Int64` / `atomic.Uint64`, nunca `atomic.AddInt64(&campo, …)` sobre campos sueltos (en ARM de 32 bits pueden quedar mal alineados y provocar un pánico). `int64` explícito para marcas de tiempo, duraciones, contadores y tamaños; nunca asumir que `int` es de 64 bits.

## Arranque y operación
Flags con su variable de entorno equivalente: `--listen` / `QAPHI_LISTEN` (recurso de emergencia; por defecto, lo configurado en la web o `:9077`), `--data-dir` / `QAPHI_DATA_DIR` (`./data`), `--log-level` / `QAPHI_LOG_LEVEL` (`info`), `--log-format` / `QAPHI_LOG_FORMAT` (`text` o `json`), `--flush-interval` / `QAPHI_FLUSH_INTERVAL` (`5m`, entre 10 s y 1 h). Logs con `slog` a stdout, **en inglés y con nombres de campo en ASCII**; bajo journald (`JOURNAL_STREAM`) se omite la marca de tiempo propia. En `info` solo se registran cambios de estado, nunca cada resultado (journald también escribe en la microSD). Planificador con jitter, concurrencia máxima configurable, timeout en todo check y recuperación de pánicos. Cabeceras de seguridad (CSP sin scripts en línea ni orígenes externos, `X-Frame-Options`, `nosniff`); secretos nunca en los logs.

## Fases (detente al final de cada una y espera confirmación)
0. **Esqueleto** ✔ (validado con emulación; pendiente validar en un ARMv7 real).
1. **Autenticación y núcleo** ✔ (`v0.1.0`; pendiente probar la instalación en la NanoPi): setup, login, sesiones, `reset-password`; checks HTTP y TCP; planificador y buffer; `/home` básico; instancia única; `install` / `uninstall` / actualización con vuelta atrás; comprobación del reloj.
2. **Ajustes, idiomas, temas y base de JavaScript** ✔ (`v0.2.0`; pendiente probar en la NanoPi): logs y consola en inglés; idiomas (es, en, de, fr) con selector en `/setup` y `/admin`; temas modulares (Automático, Claro, Oscuro); zona horaria; red (dirección, puerto 9077, HTTP/HTTPS autofirmado, confirmación de 2 min con vuelta atrás); página de ajustes; base de JavaScript (estructura, CSP, actualización en vivo de `/home` por JSON). Criterio: cambiar idioma y tema y verlos en todas las páginas; pasar a HTTPS en otro puerto y confirmar; repetir sin confirmar y comprobar que vuelve sola a la configuración anterior.
3. **Checks y estado** (`v0.3.0`): resto de checks, confirmación, incidencias, agregados, uptime, retención; componentes visuales dibujados en el navegador y página pública.
4. **Avisos e integración** (`v0.4.0`): canales, dependencias, mantenimiento, API y tokens, `status`, exportar/importar, sección Diseño.
5. **Pulido** (`v1.0.0`): revisión de seguridad, medición real de binario y RAM (optimizar si no se cumplen los goals), README, `install.sh` y publicación de binarios en GitHub Releases.

## Decisiones tomadas
- Solo Linux; arquitecturas amd64, arm64, armv7 y armv6.
- Go 1.27 (fijado en `go.mod`), módulo `github.com/future-chocolate6122/qaphi`. Licencia MIT.
- SQLite: WAL, `locking_mode=EXCLUSIVE`, `synchronous=FULL`, una conexión, caché de 2 MiB.
- Instancia única por sistema, autocontenida (bloqueo de SQLite + socket abstracto `@qaphi`).
- Instalación con `qaphi install` (instalar y actualizar), solo systemd, usuario `qaphi` sin contraseña ni shell, vuelta atrás automática, `uninstall` que conserva datos salvo `--purge`.
- Ping: `CAP_NET_RAW` concedido en la unit generada; sin `CAP_NET_BIND_SERVICE`.
- Red: un único puerto (9077 por defecto, ≥ 1024), HTTP o HTTPS autofirmado, cambios con confirmación de 2 min. Las instalaciones de `v0.1.0` que ya existan conservan su puerto 8091 al actualizar.
- Idioma, tema y zona horaria: ajustes globales de la instalación, en `/admin`.
- Autenticación: contraseña de 10 caracteres como mínimo (72 bytes como máximo, límite de bcrypt), bcrypt con coste 10 y comprobaciones serializadas; sesiones de 7 días (ajuste `session.ttl`) guardadas con hash; 5 intentos fallidos en 15 min bloquean la IP 15 min; CSRF con token de sesión (o cookie en login y setup) más comprobación de origen.
- Planificador: concurrencia 8 por defecto (ajuste `scheduler.concurrency`), margen de 5 s sobre el tiempo máximo antes de abandonar un check, y el intervalo debe superar el tiempo máximo del check.
- Los formularios envían un campo oculto «false» antes de cada casilla; un booleano ausente toma su valor por defecto.
- Nombres que se guardan (IDs de módulos, claves de campos y de ajustes, variables CSS de los temas) son estables: inglés, snake_case, sin renombrar sin migración.
- Antes de la API: identificador estable de cada check. Antes de los avisos: decidir cómo se guardan los secretos. URLs y formatos públicos (`/hb/<token>`, API, webhook, exportación con versión de formato) se documentan como estables.
- Idiomas: si el navegador no pide ninguno de los cuatro, se usa inglés. El idioma elegido en `/setup` queda como ajuste global. Los textos de cada módulo se registran con `msg.Tr(es, en, de, fr)`; una traducción vacía impide arrancar. Los mensajes de resultado se guardan codificados (`["clave","arg"]`) y se traducen al mostrarlos; los de versiones anteriores se muestran tal cual.
- Temas: variables estables `color-scheme`, `bg`, `surface`, `text`, `muted`, `border`, `accent`, `accent-text`, `danger`, `banner-text`, `status-up`, `status-slow`, `status-down`, `status-unknown`, `status-paused`, `radius`, `space`, `font`, `mono`. Cada tema se sirve en `/theme/<id>.css` con huella para caché; `style.css` no lleva colores fijos.
- Red: si el puerto nuevo es distinto, la dirección anterior y la nueva conviven hasta confirmar; si es el mismo (p. ej. solo cambia el protocolo), se sustituye en el sitio y, sin confirmación, se vuelve a la anterior. La configuración solo se guarda al confirmar (un reinicio con un cambio pendiente arranca con la anterior). Certificado ECDSA P-256 autofirmado, válido 5 años, para el nombre del equipo, `localhost` y sus IP; se puede regenerar en caliente. Los errores de handshake TLS (navegadores que aún no aceptan el certificado) van a nivel debug. `--listen` fija la dirección en HTTP y bloquea los ajustes de red; `sudo qaphi install --listen=` quita esa anulación.
- El instalador lee del log de arranque (`msg="qaphi started" listen=… scheme=…`) la dirección y el protocolo reales para comprobar que el servicio responde.
- JavaScript: un único fichero propio (`static/js/live.js`, sin librerías) que consulta `/home/state.json` cada 10 s solo con la pestaña visible; el JSON ya va traducido y formateado. Sin JavaScript, `/home` se recarga cada 30 s.
- Versiones con etiquetas semver: cada fase cerrada es `v0.N.0`, correcciones `v0.N.x`, y `v1.0.0` al terminar la fase de pulido. El Makefile toma la versión de `git describe`; sin etiqueta exacta es una compilación de desarrollo.

## Reglas de trabajo
- Al cerrar cada fase: `go build ./...`, `go vet ./...` y `go test ./...` en verde, tamaño del binario `linux/arm` (`GOARM=7`) y resumen de decisiones. Luego **para y espera**.
- No adelantes funciones de fases posteriores ni añadas dependencias (Go o JavaScript) sin consultar.
- Ante ambigüedades de diseño, pregunta; en detalles menores, decide y avísalo.
- Tests: checks con `httptest`, `Store` con base de datos temporal y lógica de estado con tests de tabla. Un test exige que todas las claves de texto existan en los cuatro idiomas.
