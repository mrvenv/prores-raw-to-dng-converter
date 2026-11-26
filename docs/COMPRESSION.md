# Compression

Pick the mode in the app's **Compression** menu, or with `-compression` on
the [command line](CLI.md). The mode's name also ends the output folder and frame names:
`clip_12bit/clip_12bit_000000.dng`; a ratio's `:` is written `to`
(`clip_resolve-8to1`).

## Which mode to choose

| In the app | CLI name | Opens in | Quality | Size (5.7K frame) |
| --- | --- | --- | --- | --- |
| Uncompressed (16-bit) | `none` | everything | original | 35 MB |
| Lossless JPEG (16-bit) | `lossless` | everything | original, bit-exact | 18 MB |
| JPEG 12-bit (visually lossless), default | `12bit` | everything | visually original | 13 MB |
| JPEG 3:1, 5:1, 8:1 | `3:1`, `5:1`, `8:1` | everything | smooth areas may band on clean footage | 8 MB at 3:1 |
| Resolve only: DCT 3:1 … 16:1 | `resolve-3:1` … `resolve-16:1` | **DaVinci Resolve only** | fine detail and grain soften first, colour holds | 4 MB at 8:1, 1.5 MB at 16:1 |

* Grading in Resolve and want small files: a `resolve-*` mode. `resolve-8:1`
  is the safe choice; `resolve-16:1` is about the size of other converters'
  "7:1" and still keeps colour.
* Need the files in other software too (Lightroom, Photoshop, After Effects,
  macOS Preview): `12bit`, or `lossless` for an exact copy.

Sizes are averages over a Panasonic GH7 clip (5728x3024, ISO 500); they scale
with resolution, and noisier footage compresses less in the lossless modes.

## Ratios

Ratios are against uncompressed 12-bit raw, the way camera makers quote
"3:1". A ProRes RAW clip is already about 3–4:1 on that scale, so every mode
from 3:1 up loses something against the source.

The setting for a ratio is picked once, on the clip's first frame, and used
for the whole clip, so quality does not change from frame to frame.

## `3:1`, `5:1`, `8:1`

These keep the standard DNG format by storing fewer brightness steps. They
never go coarser than the clip's own noise, because that is where smooth
areas start to band and pick up colour blotches. On clean footage that limit
comes first: the log says which ratio was reached (on a GH7 at ISO 500, 8:1
stops near 5:1). For higher ratios use a `resolve-*` mode.

## `resolve-*` modes

These store frames with lossy JPEG compression, like a photo JPEG but on the
raw sensor data. The loss goes into fine detail and grain rather than
banding, so colour holds even at 16:1.

**They only work in DaVinci Resolve.** The DNG standard allows this kind of
compression only for already-debayered images; Resolve reads it on raw
sensor data anyway, other software does not. In macOS, Lightroom, Photoshop
and most other apps these frames look black. Keep the source clip or convert
again with another mode if you need the files elsewhere.

## Exposure

Frames carry the clip's exposure, so on a
[fully supported camera](../README.md#cameras) they open in Resolve at the
same brightness as the ProRes RAW source; on other cameras check it and
adjust Exposure in Resolve's Camera Raw settings if needed. macOS previews ignore that setting and
can look darker or brighter; judge exposure in Resolve.
