# Qaphi · notas de uso del logotipo

## Construcción
- **Símbolo** (`qaphi-mark*.svg`): rejilla de 32 × 32. Anillo con centro en (14,5; 14,5), radio 10,5 y trazo de 5, abierto entre 10° y 80° (abajo a la derecha). El punto de estado tiene radio 4,5 y está a 45°, a 14,2 del centro.
- **Favicon** (`qaphi-favicon.svg`): rejilla de 16 × 16, redibujado para tamaños pequeños. Trazo de 2,5, hueco más abierto (12°–78°) y punto proporcionalmente mayor (radio 2,5), para que a 16 px el punto no se pegue al anillo y el color de estado se distinga.
- **Logotipo** (`qaphi-logo*.svg`): viewBox de 110,8 × 32. El símbolo ocupa la misma altura y el nombre empieza en x = 40. «Qaphi» está compuesto en Outfit SemiBold (600, licencia SIL OFL) con un espaciado de −12/1000 y convertido a trazados, así que no depende de ninguna fuente.
- El punto de estado es siempre `<circle id="status">`, y el nombre es `<path id="wordmark">`.

## Zona de respeto
Se toma como unidad el diámetro del punto de estado, **0,28 × la altura del símbolo** (9 de 32 unidades). Hay que dejar al menos esa distancia libre por los cuatro lados, tanto del símbolo como del logotipo.
- Logotipo a 28 px de alto → 8 px libres alrededor.
- Símbolo a 32 px → 9 px libres alrededor.

## Tamaño mínimo
- **Símbolo**: 16 px. Por debajo de 24 px, usa `qaphi-favicon.svg`.
- **Logotipo horizontal**: 20 px de alto (unos 69 px de ancho). Por debajo de ese tamaño, usa solo el símbolo.
- En la cabecera de 56 px, el tamaño recomendado es de 26 a 28 px de alto.

## Colores
| Uso | Color |
|---|---|
| Marca (anillo y nombre, versión a color) | `#2f7fd6` |
| Estado operativo (punto, versión a color) | `#2e9e5b` |
| Estado lento | `#d99a1e` |
| Estado caído | `#d64545` |
| Monocromo | `currentColor` (hereda el color del texto del tema) |

El azul `#2f7fd6` tiene un contraste de al menos 3,6:1 sobre los ocho fondos y superficies de los cuatro temas (Claro, Oscuro, Océano y Arena), por encima del 3:1 que se pide a los elementos gráficos.

Para la versión monocroma, estos son los colores de texto de cada tema:
- Claro: `#1b2130`
- Oscuro: `#e4e8ef`
- Océano: `#e3edf6`
- Arena: `#2a221d`

## Detalles técnicos
- `currentColor` solo hereda el color del tema si el SVG está **en línea** en el HTML (por ejemplo, `template.HTML` en Go). Si se carga con `<img>` o como favicon, se pinta en negro. En el favicon, sustituye en el servidor los dos `currentColor`: el del anillo por un color fijo y el de `#status` por el color del estado.
- Para colorear solo el estado con CSS: `#status { fill: #d99a1e }`.
- No hay `<text>`, `<image>`, filtros ni recursos externos.
- Tamaños: símbolo de 304 a 314 bytes y logotipo de 1,8 KB.
