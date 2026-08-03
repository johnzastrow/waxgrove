# Waxgrove — logo files

Concentric broken rings that read as both **record grooves** and **tree growth rings**. Every
ring carries a gap, and the gaps spiral inward — mirroring the fact that a record groove is one
continuous spiral. Nothing in the mark closes; that is the "unwalled garden" idea made literal.

The same ring language doubles as the app's **match-confidence indicator** — complete grooves
mean an exact match, fractured rings mean a record needs judgment. See
[`../direction.html`](../direction.html) §04.

## Files

| File | Use | Notes |
|---|---|---|
| `mark-full.svg` | Primary mark | 5 rings. Use at **48px and above**. |
| `mark-simple.svg` | Small sizes, app icon | 3 rings, heavier stroke. **17–48px**. |
| `favicon.svg` | Favicon, tiny UI | 2 rings. The only version that survives **16px**. |
| `mark-tile.svg` | App/launcher icon | Simple mark on a wax-black rounded tile. |
| `mark-mono.svg` | Single-colour contexts | Uses `currentColor` — see caveat below. |
| `lockup-horizontal-dark.svg` | Primary lockup, dark backgrounds | Mark + wordmark, 322×100. |
| `lockup-horizontal-light.svg` | Primary lockup, light backgrounds | "Wax" in wax-black. |
| `lockup-horizontal-mono.svg` | Single-colour lockup | `currentColor`. |
| `lockup-stacked-dark.svg` | Vertical lockup, dark | 220×176. |
| `lockup-stacked-light.svg` | Vertical lockup, light | |

## Size guidance — verified, not guessed

Rendered at true pixel sizes and inspected:

- **16px** — `mark-full` and `mark-simple` both turn to mud. Use `favicon.svg`.
- **32px** — `mark-simple` is clean; `mark-full` is already too fine.
- **48px+** — `mark-full` comes into its own; the five-ring spiral is legible.

Do not scale `mark-full` below 48px. The ring spacing is 8.5 units against a 100-unit viewBox,
which falls below a pixel once the mark drops under ~48px.

## Caveat: `currentColor` and `<img>`

The `-mono` files inherit their colour from CSS `color`. **This only works when the SVG is
inlined in the HTML, or used as a CSS `mask`.** Loaded through `<img src="...">` the SVG is an
isolated document, `currentColor` resolves to black, and the mark will disappear on a dark
background. For `<img>` use, pick an explicitly coloured file instead.

## Colours

| Token | Hex | Where |
|---|---|---|
| Grove 500 | `#3D5638` | outer ring |
| — | `#4E6B46` | second ring |
| Grove 300 | `#7A9670` | third and inner rings |
| Copper | `#C07B45` | accent ring and centre dot |
| Cream 100 | `#F4EEDE` | "Wax" on dark |
| Wax 900 | `#0B0C09` | tile background |

The mark deliberately avoids cream in its rings so it reads on **both** dark and light
backgrounds without a separate variant.

**Never recolour the mark to Spotify green (`#1DB954`)** or anything near it — see the brand
section in the root `README.md`.

## Wordmark

Set in **Fraunces** (SemiBold, `SOFT` 60, `WONK` 1, `opsz` 100) and **converted to outlines**,
so the lockups have no font dependency and render identically everywhere. To regenerate or edit
the wordmark you need the font itself:

- Fraunces — [github.com/google/fonts/tree/main/ofl/fraunces](https://github.com/google/fonts/tree/main/ofl/fraunces)
- Licensed **SIL Open Font License 1.1** — outlines may be embedded and redistributed freely.

Because the wordmark is outlined, editing the text means re-setting it in Fraunces and
re-converting; the SVGs contain paths, not editable type.

## Geometry

All rings are computed rather than eyeballed. Each has a **44° gap** (50° on `mark-simple`,
56° on `favicon`), and each successive ring is rotated **+35°** to produce the inward spiral.
`stroke-dasharray` is derived as `C·(360−gap)/360` and `C·gap/360` where `C = 2πr`, so the gap
subtends the same angle on every ring regardless of radius.
