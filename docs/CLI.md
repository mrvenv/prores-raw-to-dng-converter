# Command line

`rawconv-cli` does what the app does, for scripts and batch jobs. It ships
with every release: in the `cli/` folder on macOS, next to the app on Windows
and Linux.

```sh
rawconv-cli [flags] clip.mov [more clips…]
```

## Examples

```sh
rawconv-cli -o ~/Movies/CinemaDNG clip.mov                  # 12bit, all frames, with audio
rawconv-cli -compression resolve-8:1 -o out /Volumes/CARD/*.mov
rawconv-cli -compression lossless -o out clip.mov
rawconv-cli -frames 48 -audio=false -o test clip.mov         # quick test of a setting
```

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-o` | current folder | Output folder |
| `-compression` | `12bit` | `none`, `lossless`, `12bit`, `3:1`, `5:1`, `8:1`, `resolve-3:1`, `resolve-5:1`, `resolve-8:1`, `resolve-12:1`, `resolve-16:1`; see [COMPRESSION.md](COMPRESSION.md) |
| `-frames` | all | Convert only the first N frames |
| `-audio` | on | Save the audio as WAV; `-audio=false` to skip |
| `-j` | automatic | How many frames are written in parallel |
| `-version` | | Print the version and exit |

While converting, the progress line shows frames done, frames per second,
time so far and about how long is left (`120 / 450 · 6.1 fps · 0:20, ~0:54
left`); each clip ends with `done: <frames> frames -> <folder> (<time>, <fps>
fps)`. Before it starts, the log gives the estimated size of the clip's frames.

Output is the same as the app's: `<output>/<clip>_<mode>/<clip>_<mode>_000000.dng`
(see [USAGE.md](USAGE.md#what-you-get)).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Every clip converted |
| `1` | At least one clip failed; the others were still converted |
| `2` | Wrong usage: unknown flag or compression, no clips given |
