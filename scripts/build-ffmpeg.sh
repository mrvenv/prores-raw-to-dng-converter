#!/usr/bin/env bash
# Builds the ffmpeg and ffprobe that rawconv bundles, from the official source
# tarball: static, LGPL, and limited to what the pipeline uses (the prores_raw
# decoder, the MOV demuxer, the audio codecs and the filters convert runs).
# Runs on macOS, Linux and MSYS2 MINGW64 on Windows.
#
# usage: scripts/build-ffmpeg.sh <output-dir>
#
# The output directory gets ffmpeg, ffprobe (".exe" on Windows),
# ffmpeg-LICENSE.txt and ffmpeg-SOURCE.txt; the last two must ship with the
# binaries (LGPL 2.1). See CONTRIBUTING.md, "Bundled ffmpeg".
set -euo pipefail

readonly FFMPEG_VERSION="9.0.2"
readonly FFMPEG_SHA256="8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"
readonly FFMPEG_URL="https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"
# The oldest macOS the bundled binaries run on.
readonly MACOS_MIN_VERSION="12.0"

readonly CONFIGURE_FLAGS=(
	--disable-everything --disable-autodetect --disable-doc --disable-network
	--disable-x86asm --enable-static --disable-shared
	--enable-ffmpeg --enable-ffprobe --enable-swscale --enable-swresample
	--enable-decoder=prores_raw,aac,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s24be,pcm_s32le,pcm_f32le
	--enable-encoder=rawvideo,pcm_s24le
	--enable-demuxer=mov
	--enable-muxer=rawvideo,data,wav
	--enable-protocol=file,pipe
	--enable-filter=scale,format,null,anull,aformat,aresample,trim,atrim
)

main() {
	if [[ $# -ne 1 ]]; then
		echo "usage: $0 <output-dir>" >&2
		exit 2
	fi
	local out_dir
	out_dir="$(mkdir -p "$1" && cd "$1" && pwd)"
	local repo_root
	repo_root="$(cd "$(dirname "$0")/.." && pwd)"
	local work_dir="${repo_root}/third_party/src"
	local tarball="${work_dir}/ffmpeg-${FFMPEG_VERSION}.tar.xz"
	local src_dir="${work_dir}/ffmpeg-${FFMPEG_VERSION}"

	mkdir -p "$work_dir"
	fetch_source "$tarball"
	rm -rf "$src_dir"
	tar -C "$work_dir" -xJf "$tarball"

	local extra_flags=()
	case "$(uname -s)" in
	Darwin)
		export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-$MACOS_MIN_VERSION}"
		;;
	MINGW* | MSYS*)
		# Without -static the binaries need the MinGW runtime DLLs next to them.
		extra_flags+=(--extra-ldflags=-static)
		;;
	esac

	(
		cd "$src_dir"
		./configure "${CONFIGURE_FLAGS[@]}" ${extra_flags[@]+"${extra_flags[@]}"}
		make -j"$(getconf _NPROCESSORS_ONLN)"
	)

	local exe_suffix=""
	[[ -f "${src_dir}/ffmpeg.exe" ]] && exe_suffix=".exe"
	cp "${src_dir}/ffmpeg${exe_suffix}" "${src_dir}/ffprobe${exe_suffix}" "$out_dir/"
	cp "${src_dir}/COPYING.LGPLv2.1" "${out_dir}/ffmpeg-LICENSE.txt"
	write_source_note "${out_dir}/ffmpeg-SOURCE.txt"
	if ! "${out_dir}/ffmpeg${exe_suffix}" -hide_banner -decoders | grep -q " prores_raw "; then
		echo "built ffmpeg has no prores_raw decoder" >&2
		exit 1
	fi
	echo "ffmpeg ${FFMPEG_VERSION} -> ${out_dir}"
}

# fetch_source downloads the tarball unless a copy with the pinned checksum is
# already there.
fetch_source() {
	local tarball="$1"
	if [[ -f "$tarball" ]] && [[ "$(sha256 "$tarball")" == "$FFMPEG_SHA256" ]]; then
		return
	fi
	curl -fsSL --retry 3 -o "$tarball" "$FFMPEG_URL"
	local actual
	actual="$(sha256 "$tarball")"
	if [[ "$actual" != "$FFMPEG_SHA256" ]]; then
		echo "checksum mismatch for ${FFMPEG_URL}: got ${actual}" >&2
		exit 1
	fi
}

sha256() {
	if command -v sha256sum >/dev/null; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# write_source_note records where the bundled binaries come from, as the LGPL
# requires for redistribution.
write_source_note() {
	cat >"$1" <<EOF
The ffmpeg and ffprobe binaries in this package are FFmpeg ${FFMPEG_VERSION},
licensed under the GNU Lesser General Public License 2.1 (ffmpeg-LICENSE.txt).

Source: ${FFMPEG_URL}
SHA-256: ${FFMPEG_SHA256}
Configured with: ${CONFIGURE_FLAGS[*]}
EOF
}

main "$@"
