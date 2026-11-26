# Contributing

Everything a contributor needs: building, the code layout, how conversion and
compression work inside, and how releases are made. User documentation is the
[README](README.md) and [docs/](docs/). Coding rules and the working procedure
are in [AGENTS.md](AGENTS.md); they apply to people as much as to agents.

## Building

Requirements:

* Go (version in `go.mod`) and a C compiler: the GUI uses cgo/OpenGL (Xcode
  command line tools on macOS, MinGW-w64 on Windows, `libgl1-mesa-dev xorg-dev
  libxkbcommon-dev libwayland-dev` on Linux: the GUI supports both X11 and Wayland).
* ffmpeg 8.0+ with the `prores_raw` decoder. `make ffmpeg` builds the same
  minimal one the releases bundle into `third_party/ffmpeg`.

```sh
make ffmpeg           # third_party/ffmpeg
make build            # bin/rawconv (GUI), bin/rawconv-cli, bin/prrinfo
make test
RAWCONV_FFMPEG_DIR=$PWD/third_party/ffmpeg bin/rawconv
go run ./cmd/prrinfo -n 1 clip.mov   # frame header and derived DNG colour, no ffmpeg needed
```

ffmpeg is found only by `ffmpeg.Find`, in this order: `$RAWCONV_FFMPEG_DIR`,
next to the executable, its `ffmpeg/` subdirectory, `../Resources` of the app
bundle, `$PATH`, `/opt/homebrew/bin`, `/usr/local/bin`, `/opt/local/bin`.

Every ffmpeg and ffprobe process is started with `ffmpeg.Command`, never
`exec.Command` directly. On Windows it starts the process with
`CREATE_NO_WINDOW`: the GUI is a windowed app without a console, so Windows
would otherwise open a terminal window for each ffmpeg a conversion runs.

Before sending a change: `gofmt -l .` prints nothing, `go vet ./...` and
`go test -race ./...` pass, and a change to the conversion path is tried on a
few frames of a real clip (`rawconv-cli -frames 5`). Sample clips and output
are never committed.

## Layout

| Path | What it is |
| --- | --- |
| `main.go` | Desktop app (Fyne): clip list, drag and drop, output folder, progress, log |
| `cmd/rawconv-cli` | Command-line converter |
| `cmd/prrinfo` | Debug tool: ProRes RAW frame headers and the DNG colour tags derived from them |
| `internal/convert` | The pipeline; `color.go` colour matrices and exposure, `meta.go` camera metadata and `metadata.json` |
| `internal/prr` | ProRes RAW frame header (`prrf` packet) parser |
| `internal/dng` | CinemaDNG writer; `tiles.go` lossless and curve modes, `dct.go` the Resolve modes |
| `internal/ljpeg` | Lossless JPEG (T.81 SOF3) codec |
| `internal/dctjpeg` | 12-bit DCT JPEG (T.81 SOF1) codec |
| `internal/jpegbits` | What both JPEG codecs share: markers, Huffman tables, bit I/O |
| `internal/timecode` | SMPTE timecode and its DNG BCD encoding |
| `internal/ffmpeg` | Finding ffmpeg/ffprobe and probing clips |
| `internal/version` | The version linked in at build time |

## How a conversion works

```
clip.mov ──ffmpeg -c copy──▶ packets ──▶ prr.ParseHeader (WB, matrix, levels, gain)
        └─ffmpeg decode───▶ bayer_rggb16le ──▶ dng.Write ──▶ clip_12bit/clip_12bit_000000.dng …
        └─ffmpeg audio────▶ clip_12bit/clip_12bit.wav
```

`convert.Convert` probes the clip and runs two ffmpeg processes: one copies the
raw `prrf` packets, whose headers give each frame's black and white levels,
white balance, colour matrix and gain; the other decodes to `bayer_rggb16le`
with `-thread_type slice` (ffmpeg's default frame threading keeps the
`prores_raw` decoder on one core).
Frames are paired by index and written by a worker pool.

Colour: the header's camera→XYZ matrix is inverted into `ColorMatrix1`,
`AsShotNeutral` is the inverse white balance, and `BaselineExposure` is
`log2(gain) - 4`. With `log2(gain)` alone clips came out 4 stops brighter in
Resolve than the source; DNGs Resolve shows at the source's brightness carry
exactly `log2(gain) - 4`. macOS ignores `BaselineExposure`, so exposure can
only be checked in Resolve.

When the first frame's header has no usable white balance or gain,
`MeasureAuto` (`auto.go`) measures them from that frame's pixels once, for
the whole clip: white balance by grey world (the average colour, without
clipped and near-black sensels, is neutral), exposure so the median green
lands where it does on the checked clips (`autoMidGreyStops`), within 4
stops of their typical `BaselineExposure`. A header's own values always win;
the log warns when auto was used. On the GH7 and Sony samples auto exposure
lands within 0.25 stops of the header's, auto white balance within 0.3 stops
(grey world drifts when one colour fills the frame).

## Compression internals

Ratios are against uncompressed 12-bit raw. A ratio mode is calibrated once,
on the clip's first frame, and that setting is used for every frame, so
quality does not flicker.

**Lossless JPEG modes** (`tiles.go`): 256x256 tiles, each coded as lossless
JPEG of pixel pairs so a predictor sees one CFA colour. The curve modes map
samples through a square-root curve starting just below black, undone by
`LinearizationTable`; on it photon noise is equal at every brightness.
`12bit` has 4096 codes; `3:1`/`5:1`/`8:1` get fewer from `CalibrateLevels`,
never below `noiseFloorLevels` (curve step at most twice the measured noise),
because coarser steps posterise and white balance turns that into colour
blotches. Every curve mode declares 12 bits with a full 4096-entry table:
macOS renders frames under 8 bits black and Resolve garbled an 8-bit frame
with a short table.

**Resolve modes** (`dct.go`): the 12-bit curve as DCT JPEG (SOF1), quantised
with T.81 table K.1 scaled by `CalibrateQuant`. DNG allows DCT only for
demosaiced images; Resolve reads it in CFA tiles anyway. The layout copies
CinemaDNG files Resolve is known to read: tiles half the frame wide and 304
rows high, `BitsPerSample` 16, each tile one two-component JPEG of half its
rows, so a JPEG row holds two CFA rows and every 8x8 block sees one CFA colour.
macOS and Adobe show these frames black.

## Releasing

rawconv is built and published by GitHub Actions. Nobody builds a release by
hand: a push to `main` or `dev` is the release.

**For now the automatic triggers are commented out** (`on:` in both
workflows): CI and Release run only by hand, from the Actions tab → Run
workflow, picking `main` for a release or `dev` for a beta. The tables below
describe the setup once the triggers are back on.

### Branches

| Branch | A push publishes | Example tag |
| --- | --- | --- |
| `main` | A release, marked latest | `v0.2.0` |
| `dev` | A pre-release (beta) | `v0.2.0-beta.57` |
| PR into `main` or `dev` | Nothing: CI only (gofmt, vet, tests) | — |

Work lands in `dev` and is published as betas; merging `dev` into `main`
publishes the release.

### Version

The version lives in one place: the `VERSION` file at the repository root, a
single `x.y.z` line. It is the version of the **next** release.

- A release from `main` is tagged `v<VERSION>`.
- A beta from `dev` is tagged `v<VERSION>-beta.<N>`, where `N` is the run number
  of the Release workflow: it only grows, so betas sort in the order they were
  built.
- Once `v<VERSION>` is released, a further push to `main` or `dev` fails in the
  "Resolve version" step until `VERSION` is bumped. This keeps a released
  version final and stops betas of an already released version.

So, after a release: bump `VERSION` in `dev` first (`0.2.0` → `0.3.0`), then
keep working.

The version reaches the binaries through
`-ldflags -X=rawconv/internal/version.Version=<version>`: the GUI shows it in
the window title, `rawconv-cli -version` prints it. `go run` builds show `dev`,
`make build` builds `<VERSION>-dev`. The macOS bundle and the Windows exe carry
only the numeric `x.y.z` (bundles reject a suffix) plus the run number as the
build number.

### What a release contains

| Archive | Built on | Contents |
| --- | --- | --- |
| `rawconv-<v>-macos-arm64.zip` | `macos-latest` | `ProRes Raw to DNG Converter.app` (ffmpeg in `Contents/Resources`), `cli/` (`rawconv-cli`, `prrinfo`, ffmpeg) |
| `rawconv-<v>-macos-amd64.zip` | `macos-15-intel` | same |
| `rawconv-<v>-windows-amd64.zip` | `windows-latest` (MSYS2 MINGW64) | `ProRes Raw to DNG Converter.exe`, `rawconv-cli.exe`, `prrinfo.exe`, `ffmpeg.exe`, `ffprobe.exe` |
| `rawconv-<v>-linux-amd64.tar.xz` | `ubuntu-22.04` | `rawconv`, `rawconv-cli`, `prrinfo`, `ffmpeg`, `ffprobe` |
| `SHA256SUMS.txt` | | Checksums of the archives |

Fyne needs cgo, so each platform is built on its own runner rather than
cross-compiled. ffmpeg sits where `ffmpeg.Find` looks for it: next to the
executable, or in `../Resources` of the app bundle.

The macOS app is not signed or notarized; the release notes tell users to run
`xattr -dr com.apple.quarantine "ProRes Raw to DNG Converter.app"` once.

### Bundled ffmpeg

`scripts/build-ffmpeg.sh` builds ffmpeg and ffprobe from the official source
tarball, pinned by version and SHA-256: static, LGPL (no `--enable-gpl`), with
only the decoders, demuxer, muxers and filters the pipeline uses. Each archive
ships `ffmpeg-LICENSE.txt` (LGPL 2.1) and `ffmpeg-SOURCE.txt` (source URL,
checksum, configure flags), as the LGPL requires. `--disable-autodetect` turns
threading off too, so the script enables it per platform (pthreads, Win32
threads on Windows): without it the decoder runs on one core.

CI caches the build per platform, keyed by the script's hash: changing the
version, the flags or the script rebuilds it. When the pipeline starts using a
new codec, muxer or filter, add it to `CONFIGURE_FLAGS` in the script.

### Building an archive locally

```bash
go install fyne.io/tools/cmd/fyne@latest
make ffmpeg     # third_party/ffmpeg
make dist       # dist/rawconv-<VERSION>-dev-<os>-<arch>.zip / .tar.xz
```

On Windows run these from an MSYS2 MINGW64 shell with `make`, `zip`, `curl`
and `mingw-w64-x86_64-gcc` installed.

### Release files

| File | Role |
| --- | --- |
| `VERSION` | The next release's version |
| `internal/version` | The variable the version is linked into |
| `FyneApp.toml`, `Icon.png` | App name, ID and icon for `fyne package` |
| `scripts/build-ffmpeg.sh` | Builds the bundled ffmpeg |
| `scripts/package.sh` | Builds the archive for the current platform |
| `.github/workflows/ci.yml` | gofmt, vet, `go test -race` on PRs and before every release |
| `.github/workflows/release.yml` | Version, build matrix, GitHub release |
