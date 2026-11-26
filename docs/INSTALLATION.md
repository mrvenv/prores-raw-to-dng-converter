# Installation

Download the archive for your system from the
[Releases page](https://github.com/mrvenv/prores-raw-to-dng-converter/releases) on GitHub. Every
archive includes everything rawconv needs, ffmpeg too: there is nothing else
to install. Versions marked *Pre-release* are betas.

| System | Archive |
| --- | --- |
| macOS, Apple silicon (M1 and later) | `rawconv-<version>-macos-arm64.zip` |
| macOS, Intel | `rawconv-<version>-macos-amd64.zip` |
| Windows 10/11, 64-bit | `rawconv-<version>-windows-amd64.zip` |
| Linux, 64-bit | `rawconv-<version>-linux-amd64.tar.xz` |

## macOS

Requires macOS 12 Monterey or later.

1. Unzip the archive.
2. Move **ProRes Raw to DNG Converter.app** to Applications.
3. The app is not signed by Apple, so the first launch is blocked ("damaged"
   or "cannot be opened"). Either right-click the app → **Open** → **Open**,
   or run once in Terminal:

   ```sh
   xattr -dr com.apple.quarantine "/Applications/ProRes Raw to DNG Converter.app"
   ```

The `cli/` folder holds the command-line tool, see [CLI.md](CLI.md). The app
does not need it.

## Windows

1. Unzip the archive to a folder of its own. Keep the files together: the app
   finds `ffmpeg.exe` next to it.
2. Start **ProRes Raw to DNG Converter.exe**. The app is not signed, so Windows may show
   "Windows protected your PC": click **More info** → **Run anyway**.

## Linux

1. Unpack: `tar -xJf rawconv-<version>-linux-amd64.tar.xz`
2. Run `./rawconv` from the unpacked folder. Keep the files together: the app
   finds `ffmpeg` next to it.

The app needs a desktop with OpenGL (any usual X11 or Wayland desktop).

## Checking the download

`SHA256SUMS.txt` on the [release page](https://github.com/mrvenv/prores-raw-to-dng-converter/releases) lists a checksum for every archive:

```sh
shasum -a 256 rawconv-<version>-macos-arm64.zip   # Linux: sha256sum
```

## Updating and uninstalling

To update, download the new version from the
[Releases page](https://github.com/mrvenv/prores-raw-to-dng-converter/releases) and replace the old app or folder. To
uninstall, delete it. The app remembers your output folder and compression in
the system's app preferences; they do no harm if left behind.

Next: [converting clips](USAGE.md).
