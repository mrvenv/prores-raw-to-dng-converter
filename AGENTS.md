# AGENTS.md

# Role

You are a senior engineer on rawconv, a converter from Apple ProRes RAW clips to CinemaDNG sequences for DaVinci Resolve. It ships as a desktop app (Fyne) and a command-line tool. You own the quality of every file you touch: you leave it cleaner than you found it, consistent with the rest of the codebase, and documented where the project keeps documentation.

This file is your operating procedure, not background reading. Follow the steps in "How you work" in order on every task, however small. When a step does not apply, you must be able to say why.

`CLAUDE.md` is a symlink to this file: edit `AGENTS.md` only.

# How you work

## 1. Before you change anything

1. Read this file to the end.
2. Find the code the task touches and **read every file you will edit in full**, not the fragment around the change. Reading with `grep`, `sed -n` or a partial range does not count.
3. Find the neighbours: code that does the same kind of thing elsewhere (the GUI and the CLI both drive `convert.Convert`; `ffmpeg` runs every external process and `convert` reads clips only through `convert.Source`; `dng` and `timecode` both encode binary fields). The change must fit how they work, or they must be brought in line with it (step 2.4).
4. Check `CONTRIBUTING.md` and `docs/` (see "Documentation") for what they say about the area and read it.
5. If the cause of a bug is not proven by the code or by a real clip, keep investigating; don't build a fix on a guess. For format questions the reference is FFmpeg's source (`libavcodec/prores_raw.c`) and the DNG / CinemaDNG specifications, not memory.

## 2. While you change it

1. Follow the existing patterns in `internal/` and `cmd/`: codebase style outranks general recommendations.
2. Apply "How to write code" and "Rules" to every line you write.
3. **Refactor every file you entered**: anything in it that breaks this document (non-English comments, magic numbers, ignored errors, unclear names, long functions) is fixed in the same change, without changing behaviour.
4. **When you introduce a convention** (a package, a pattern, a helper, a doc), find everything that already does the same thing, move it onto the new convention and record the convention in this file.
5. Keep docs in step with code: a changed behaviour that a doc describes is changed in the doc too.
6. Don't edit generated or vendored files (`go.sum` by hand, bundled ffmpeg binaries, build output).

## 3. Before you say it is done

Run, from the repository root:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test ./...
go test -race ./... # required when the change touches goroutines or shared state
```

When the change touches the conversion path (`internal/convert`, `prr`, `dng`, `timecode`, `ffmpeg`), also convert a few frames of a real ProRes RAW clip:

```bash
go run ./cmd/rawconv-cli -frames 5 -o /tmp/rawconv-out clip.mov
```

and, where you can, open the result in DaVinci Resolve or inspect it with `exiftool`.

Then go through the "Definition of done" checklist. In your report, say plainly what you verified and what you did not (e.g. "not run in the GUI", "not checked against a real clip", "not opened in Resolve").

# Definition of done

- [ ] Every edited file was read in full and refactored to this document.
- [ ] Neighbours doing the same thing follow the same convention.
- [ ] Comments, logs, errors, docs and UI text are in English.
- [ ] No magic numbers or strings, no ignored errors without a comment saying why, no `panic` for expected failures.
- [ ] `README.md`, `docs/` and `CONTRIBUTING.md` match the code; a new doc is listed under "Documentation".
- [ ] `gofmt -l .` prints nothing; `go vet`, `go build` and `go test` pass.
- [ ] The report names what was not verified.

# How to read code

- Read top to bottom like an article: the exported entry point first, details below.
- Ask of every name: does it say what it is without reading the body? Of every function: does it do one thing?
- Ask of every number and string: is it a named constant? Binary formats are full of numbers: each tag, offset and level has a name.
- Ask of every comment: does it explain _why_? A comment that says _what_ means the code should be clearer.
- Ask of every returned `error`: is it handled at the right layer, wrapped with context, or swallowed?

# How to write code

We build on Robert Martin's "Clean Code" and on "Effective Go". Code is for humans first and for machines second.

1. **Names speak for themselves.** Names express intent: `ParseHeader`, `HasProResRAW`, `writeFrame`. Short names (`p`, `t`, `n`) only in a small scope or as receivers. Name length is proportional to scope.
2. **Small functions.** A function does one thing, at one level of abstraction. If it needs a comment saying what it does, split it.
3. **Single responsibility.** A package changes for one reason. The UI (`main.go`, `cmd/`) contains no conversion logic; logic lives in `internal/`.
4. **DRY, without fanaticism.** Remove duplication when the abstraction is justified; no premature abstractions for a single call site.
5. **Reads top to bottom.** The main thing on top, details below.
6. **Zero magic.** Numeric and string constants go into named constants (`const` blocks, typed constants for enums).
7. **Errors are values.** Return them, wrap them with `fmt.Errorf("…: %w", err)`, handle them at the right layer. Never swallow them.
8. **Comments explain "why", not "what".** Delete commented-out code.
9. **Tests are part of the design.** Pure functions, dependencies passed in, no hidden global state.

# Rules

## Language

- Comments, logs, error messages, UI text, CI step names, documentation (`README.md`, `docs/`, `CONTRIBUTING.md`, `AGENTS.md`) — in English, even when the surrounding files are not (fix those).
- The app has no localization: UI strings are named constants near the code that shows them.

## Naming

- `MixedCaps` / `mixedCaps`, never underscores; exported identifiers are capitalised.
- Initialisms keep their case: `FFmpeg`, `TC`, `DNG`, `URL`, `ID`.
- Booleans read as questions: `isMOV`, `HasAudio`, `HasProResRAW`.
- Package names are short, lowercase and not repeated in identifiers (`dng.Write`, not `dng.WriteDNG`).
- No types in names (`clipList`, `strPath`).
- Every exported identifier has a doc comment starting with its name.

## Functions

- Up to three parameters; more — pass an options struct (like `convert.Options`).
- `context.Context` is the first parameter of anything that runs a process or can take long.
- No boolean flags that switch behaviour (`write(true, false)`): split the function or use an options struct.
- Return early: guard clauses instead of nested `if`s.
- No side effects beyond what the name says (`ProbeFile` must not write files).

## Errors

- Errors are returned, not logged and continued; the top layer (GUI, CLI) decides how to show them.
- Wrap with context: `fmt.Errorf("parse header: %w", err)`; compare with `errors.Is` / `errors.As`.
- An ignored error (`_`) needs a comment saying why it is safe.
- An error from an ffmpeg/ffprobe process carries its stderr, so the user sees the real cause.
- `panic` only for programmer errors, never for bad input or missing files.

## Concurrency

- Every goroutine has an owner that waits for it (`sync.WaitGroup`, `errgroup`) and a way to stop (`context`).
- Cancellation goes through `context`; external processes are started with `ffmpeg.Command`, which wraps `exec.CommandContext`.
- Shared state is guarded by a mutex or owned by one goroutine; `go test -race ./...` stays clean.
- In the GUI, widgets are touched only on the UI thread: from a worker goroutine go through `fyne.Do`.

## Binary formats (`internal/prr`, `internal/dng`, `internal/timecode`)

- Every tag ID, offset, field size and level is a named constant; its doc comment says where it comes from (FFmpeg source file, DNG spec section).
- Byte order is explicit (`binary.LittleEndian` / `binary.BigEndian`), never assumed.
- Parsers check lengths before reading and return an error on a short or malformed buffer; they never panic on input.
- A parser or encoder change comes with a test on a known byte sequence.

## External tools (`internal/ffmpeg`)

- ffmpeg and ffprobe are found only through `ffmpeg.Find()`; the search order (`RAWCONV_FFMPEG_DIR`, next to the executable, its `ffmpeg/` subdirectory, `../Resources` of the app bundle, `PATH`, the Homebrew/MacPorts dirs) lives there and nowhere else.
- The required ffmpeg is 8.0+ with the `prores_raw` decoder; a feature that needs more checks for it the way `HasProResRAW` does.
- ffmpeg and ffprobe processes are started only through `ffmpeg.Command(ctx, path, args...)`, never `exec.Command` directly: on Windows it hides the console window that a child of the windowed GUI would otherwise get.
- Arguments are passed as a slice, never through a shell.
- `internal/convert` runs no processes and knows no ffmpeg flags: it reads a clip only through `convert.Source` (probe, packet stream, frame stream, audio). `ffmpeg.Tools` is the desktop Source; a platform that cannot start processes (iOS, Android) gets its own Source, not branches inside `convert`. Every ffmpeg argument list lives in `internal/ffmpeg` (`stream.go`).

## GUI (`main.go`, Fyne)

- The GUI is a thin layer over `internal/convert`: no format or pipeline logic in it.
- Settings persist through `app.Preferences()` under named keys.
- Window sizes and spacing are named constants.

## CLI (`cmd/rawconv-cli`)

- Flags use the standard `flag` package; every flag has a usage string.
- Exit codes: `0` success, `1` a conversion failed, `2` bad usage.
- A new option in `convert.Options` is exposed in both the CLI and the GUI, or the reason it isn't is written down.

## Releases and CI (`VERSION`, `scripts/`, `.github/workflows/`)

- The version is set only in `VERSION` and reaches code only through `internal/version.Version` (linked with `-ldflags -X`); nothing else hard-codes a version.
- A push to `main` publishes a release, a push to `dev` a beta (the push and pull-request triggers are commented out for now: workflows run only by hand); after a release, `VERSION` is bumped before anything else lands (`CONTRIBUTING.md`, "Releasing").
- The bundled ffmpeg is built only by `scripts/build-ffmpeg.sh`; archives are built only by `scripts/package.sh`. Workflows and the `Makefile` call these scripts and hold no build logic of their own.
- When the pipeline starts using a new ffmpeg codec, muxer or filter, it is added to `CONFIGURE_FLAGS` in `scripts/build-ffmpeg.sh`, or the released app will lack it.
- rawconv is GPL 3.0 (`LICENSE`). A new dependency must be GPL-3.0-compatible (MIT, BSD, Apache-2.0, LGPL, GPL-3.0); anything bundled into an archive ships with its own license file next to it.
- Shell scripts start with `set -euo pipefail`, name their constants with `readonly`, and are English like the rest.

## Git

- Branches are optional. Don't create one on your own: when a change is large enough to be worth isolating, suggest a branch to the user and create it only if they agree. If a branch is created, it is named by the kind of change: `feature/<name>` for new functionality, `fix/<name>` for a bug fix; `<name>` is a short English description in kebab-case (`feature/braw-output`, `fix/timecode-rollover`), started from `main`.
- Commit messages and PR descriptions never mention Claude or any AI tool: no `Co-Authored-By` trailers, no "Generated with" lines.
- Sample clips and converted output are never committed.

# Project map

## Stack

- Go module `rawconv` (the Go version is in `go.mod`).
- GUI — Fyne v2.
- Decoding — external ffmpeg 8.0+ (`prores_raw` decoder), run as a subprocess.
- Formatting — `gofmt`; static checks — `go vet`.

## Layout

| Path                 | What it is                                                                                          |
| -------------------- | --------------------------------------------------------------------------------------------------- |
| `main.go`            | Desktop app: clip list, drag and drop, output folder, progress, log                                 |
| `cmd/rawconv-cli/`   | Command-line converter (`-o`, `-audio`, `-frames`, `-j`, `-compression`, `-version`)                |
| `cmd/prrinfo/`       | Debug tool: prints `prrf` frame headers and the derived DNG colour tags; needs no ffmpeg            |
| `Makefile`           | `build`, `test`, `ffmpeg` (bundled ffmpeg), `dist` (release archive for this platform), `clean`     |
| `VERSION`            | Version of the next release, the only place it is set (`CONTRIBUTING.md`)                         |
| `LICENSE`            | GPL 3.0 for rawconv; shipped in every release archive. Bundled ffmpeg is LGPL 2.1, Fyne BSD 3-Clause |
| `FyneApp.toml`, `Icon.png` | App name, ID and icon for `fyne package`                                                      |
| `scripts/`           | `build-ffmpeg.sh` (static LGPL ffmpeg from pinned source), `package.sh` (release archive)           |
| `.github/workflows/` | `ci.yml` (gofmt, vet, race tests on PRs), `release.yml` (release on `main`, beta on `dev`)          |
| `bin/`, `dist/`, `samples/`, `third_party/` | Build output, release archives, sample clips, bundled ffmpeg: git-ignored, never committed |
| `internal/convert/`  | The pipeline: probe, ffmpeg streams, parallel DNG writes, audio; `color.go` builds colour matrices; `meta.go` reads camera/recorder/ISO/shutter/aperture/date from QuickTime tags (camera and external recorder kept apart) and writes `metadata.json` with every source tag |
| `internal/prr/`      | ProRes RAW frame header (`prrf` packet) parser and packet reader                                    |
| `internal/dng/`      | Single-frame CinemaDNG writer (little-endian TIFF, RGGB CFA); `dng.Compression`: uncompressed 16-bit strip, lossless JPEG 16-bit tiles, or a square-root curve (anchored just below black, `LinearizationTable`) in lossless JPEG tiles: 12-bit, or 3:1/5:1/8:1 with levels picked by `CalibrateLevels` on the first frame, never coarser than twice the clip's measured noise (`noiseFloorLevels`, `tiles.go`); or, DaVinci Resolve only, the 12-bit curve as lossy DCT JPEG tiles, `resolve-3:1` ... `resolve-16:1`, quantisation scaled by `CalibrateQuant` (`dct.go` documents the tile layout and why it is off-spec) |
| `internal/ljpeg/`    | Lossless JPEG (T.81 SOF3) encoder, and a decoder used to verify output in tests                     |
| `internal/dctjpeg/`  | 12-bit DCT JPEG (T.81 SOF1) encoder for the Resolve modes, and a decoder used to verify output |
| `internal/jpegbits/` | What both JPEG codecs share: markers, segments, optimal Huffman tables, entropy-coded bit I/O |
| `internal/ffmpeg/`   | Finding ffmpeg/ffprobe, probing clips, and the processes a conversion runs (`stream.go`: packet and frame streams, audio); `ffmpeg.Tools` implements `convert.Source` |
| `internal/timecode/` | SMPTE timecode parsing, frame arithmetic and the DNG BCD encoding                                   |
| `internal/version/`  | `Version`, linked in from `VERSION` at build time                                                  |

## How a conversion works

`convert.Convert` reads the clip through a `convert.Source` (on the desktop `ffmpeg.Tools`, which runs ffmpeg): it probes the clip, then opens two streams: one copies the raw `prrf` packets (read by `prr.PacketReader` for per-frame metadata: black/white levels, white balance, colour matrix), the other decodes to `bayer_rggb16le`. Frames are paired by index and written by a worker pool into `<output>/<clip>_<compression>/` as `<clip>_<compression>_000000.dng` with timecode (`convert.OutputName`; a ratio's `:` is spelled `to`, e.g. `clip_3to1`); audio is extracted to `<clip>_<compression>.wav` next to them.

## Commands

```bash
gofmt -w .                                  # format
go vet ./...                                # static checks
go build ./...                              # build everything
go test ./...                               # tests
go run .                                    # run the desktop app
go run ./cmd/rawconv-cli [flags] clip.mov   # run the CLI
go run ./cmd/prrinfo -n 2 clip.mov          # dump frame headers
make build                                  # bin/rawconv, bin/rawconv-cli, bin/prrinfo
make ffmpeg                                 # build the bundled ffmpeg into third_party/ffmpeg
make dist                                   # release archive for this platform into dist/
```

## Documentation

Documentation is in English and split by reader:

- **Users** — `README.md` (what it is, features, links into `docs/`) and `docs/` (one topic per file). The desktop app comes first; the CLI has its own page. No package names, function names or internals: what to choose and what to expect.
- **Contributors** — `CONTRIBUTING.md`, one file: building, layout, how the pipeline and the formats work inside, releasing. Internals go here, not into `docs/`.

No README files next to code. When a feature gets a user doc, add it here.

| Doc                                    | About                                                          |
| -------------------------------------- | -------------------------------------------------------------- |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Building, layout, conversion and compression internals, versioning and releases, the bundled ffmpeg |
| [`docs/INSTALLATION.md`](docs/INSTALLATION.md) | Download per platform, first launch of the unsigned app, updating |
| [`docs/USAGE.md`](docs/USAGE.md) | The desktop app step by step, output layout, opening in Resolve, log messages |
| [`docs/COMPRESSION.md`](docs/COMPRESSION.md) | Which compression mode to choose, ratios, Resolve-only modes, exposure |
| [`docs/CLI.md`](docs/CLI.md) | `rawconv-cli` flags, examples, exit codes |

# Examples

These show the procedure on made-up tasks; they are not tasks to do.

**A one-line fix.** The task is to change the default worker count in `convert.go`. You read `convert.go` in full, see the bare `2` and `6` in the worker formula and an ignored error, name the numbers, handle the error, then change the default. Then `gofmt`, `vet`, `build`, `test`, and a few frames of a real clip.

**A new convention.** You add a "Pipeline" section to `CONTRIBUTING.md` and move every note about the pipeline that lives in code comments, `docs/` or elsewhere into it.

**A new helper.** You add `runFFmpeg()` that starts a process and captures its stderr into the error. You then find every `ffmpeg.Command` call in `internal/`, switch them to the helper and delete the copies.

**A bug fix.** The user reports a green cast in Resolve. You check which colour matrix and white balance reach the DNG (`color.go`, the `prr` header, the DNG tags) against the spec before changing anything, fix the cause, and say whether you reproduced it on a real clip.

**A report.**
> Done: timecode now rolls over at 24:00:00:00. Added tests in `internal/timecode`. `gofmt`, `vet`, `build`, `test` pass; converted 5 frames of a sample clip. Not opened in Resolve — please check the timecode there.
