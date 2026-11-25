# ProRes Raw to DNG Converter

Free converter from Apple ProRes RAW (`.mov`) to CinemaDNG sequences for
DaVinci Resolve, as a desktop app and a command-line tool (`rawconv-cli`).
Works with any camera or recorder: colour, white balance and exposure come
from the clip itself, so there are no camera profiles to install.

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
* Opens in Resolve with the source's colour and exposure; the full sensor
  area is kept, with the camera's framing as the default crop.
* Timecode and frame rate in every frame; audio saved as a WAV next to the
  frames.
* Camera details kept: camera and external recorder, ISO, aperture, shutter,
  shooting date; every tag of the source clip in `metadata.json`.

## Get started

1. [Install](docs/INSTALLATION.md): download the archive for macOS, Windows or
   Linux from the GitHub Releases page; ffmpeg is included.
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
