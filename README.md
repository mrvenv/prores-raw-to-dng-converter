# ProRes Raw to DNG Converter

Free converter from Apple ProRes RAW (`.mov`) to CinemaDNG sequences for
DaVinci Resolve, as a desktop app and a command-line tool (`rawconv-cli`).
Colour, white balance and exposure come from the clip itself, so there are
no camera profiles to install; see [Cameras](#cameras) for which models are
checked.

## Features

* Drop clips or whole folders, pick an output folder and a compression,
  convert. Convert again to make another version, e.g. with a different
  compression.
* Compression from a bit-exact copy to about 16:1, see
  [docs/COMPRESSION.md](docs/COMPRESSION.md):
  * Uncompressed, Lossless, JPEG 12-bit (default), JPEG 3:1 / 5:1 / 8:1:
    standard DNG, opens everywhere (Resolve, Lightroom, Photoshop, macOS).
  * Resolve only: DCT 3:1 … 16:1: much smaller files that keep colour,
    **DaVinci Resolve only**.
* Each clip and mode gets its own folder,
  `<output>/<clip>_<mode>/<clip>_<mode>_000000.dng`, so versions of one clip
  sit side by side.
* Opens in Resolve with the source's colour and exposure on
  [checked cameras](#cameras); the full sensor
  area is kept, with the camera's framing as the default crop.
* Timecode and frame rate in every frame; audio saved as a WAV next to the
  frames.
* Camera details kept: camera and external recorder, ISO, aperture, shutter,
  shooting date; every tag of the source clip in `metadata.json`.

## Cameras

Every camera that records ProRes RAW, in camera or through an external
recorder, converts. What differs is how each one writes exposure and colour
into the clip: rawconv has to read that per camera, and until a camera has
been checked its frames can open in Resolve brighter or darker than the
source (one Lumix came out 4 stops too bright before it was fixed).

**Fully supported** — checked on real clips: exposure and colour in Resolve
match the source.

* Panasonic LUMIX GH7 (internal ProRes RAW)

**Partly supported** — converts, but exposure and colour are not checked yet
and may need adjusting by hand in Resolve (Camera Raw → Decode Using: Clip →
Exposure):

<details>
<summary>Cameras</summary>

* Sony ILCE-7SM3 (A7S III)
* Sony ILCE-7RM5 (A7R V)
* Sony ILME-FX3 (FX3)
* Sony ILME-FX6 (FX6)
* Sony ILCE-1 (A1)
* Sony ILCE-1M2 (A1 II)
* Sony ILME-FX30 (FX30)
* Sony ILCE-9M3 (A9 III)
* Sony ILCE-7CR (A7CR)
* Nikon Z6, Z6 II, Z6 III
* Nikon Z7, Z7 II
* Nikon Z8, Z9
* Z CAM E2, E2C, E2-F6, E2-M4, E2-S6, E2-S6G, E2-F8
* FUJIFILM GFX100, GFX100 II, GFX100S
* FUJIFILM X-H2S, X-H2, X-S20, X-M5
* SIGMA fp, fp L
* Canon EOS R5, EOS R5 C, EOS R6 Mark II
* DJI Zenmuse X7, Ronin 4D 6K, Ronin 4D 8K, Inspire 3
* Olympus OM-D E-M1 Mark III, OM-D E-M1X, OM System OM-1
* Panasonic LUMIX GH5S, GH6, G9 II
* Panasonic LUMIX S1, S1R II, S1H, S5, S5 II, S5 IIX, BS1H

</details>

Shooting with one of these? Send a short clip or a note on how far off it
looks through [Issues](https://github.com/mrvenv/prores-raw-to-dng-converter/issues),
and it can move up to fully supported.

## Get started

1. [Install](docs/INSTALLATION.md): download the archive for macOS, Windows or
   Linux from the [Releases page](https://github.com/mrvenv/prores-raw-to-dng-converter/releases); ffmpeg is included.
2. [Convert](docs/USAGE.md): open **ProRes Raw to DNG Converter**, drop clips or a card
   folder, pick the output folder and compression, press **Convert**.
3. Open the output folder in DaVinci Resolve's Media page.

## Documentation

| | |
| --- | --- |
| [Installation](docs/INSTALLATION.md) | Download, first launch of the unsigned app, updating |
| [Converting clips](docs/USAGE.md) | The app step by step, output folders, opening in Resolve, log messages |
| [Compression](docs/COMPRESSION.md) | Which mode to choose, file sizes, Resolve-only modes |
| [Command line](docs/CLI.md) | `rawconv-cli` for scripts and batch jobs |

## Building from source

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Copyright © 2026 tsofindavid.

rawconv is free software, released under the
[GNU General Public License v3.0](LICENSE): you may use, change and
redistribute it, but any distributed version, modified or not, must come with
its source code under the same license. Nobody can turn it into a closed
product.

Release archives bundle FFmpeg as separate `ffmpeg` / `ffprobe` executables,
under the LGPL 2.1 (`ffmpeg-LICENSE.txt`, `ffmpeg-SOURCE.txt` in each archive).
The GUI is built with [Fyne](https://fyne.io) (BSD 3-Clause).
