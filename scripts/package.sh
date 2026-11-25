#!/usr/bin/env bash
# Builds the release archive for the platform it runs on: the GUI, rawconv-cli,
# prrinfo and the bundled ffmpeg, placed where ffmpeg.Find looks for it.
# Fyne needs cgo, so every platform is built on its own machine (see
# .github/workflows/release.yml).
#
# usage: scripts/package.sh <version> <ffmpeg-dir> <dist-dir>
#
#   version     full version, e.g. 0.1.0 or 0.1.0-beta.12
#   ffmpeg-dir  output of scripts/build-ffmpeg.sh
#   dist-dir    where the archive is written
#
# BUILD_NUMBER (default 1) becomes the app bundle's build number.
#
# Layout of the archive:
#   macOS:          ProRes Raw to DNG Converter.app (ffmpeg in Contents/Resources),
#                   cli/ (rawconv-cli, prrinfo, ffmpeg)
#   Windows, Linux: the GUI, rawconv-cli, prrinfo and ffmpeg side by side
#   every archive:  LICENSE.txt (rawconv, GPL 3.0) at the top level
set -euo pipefail

# APP_NAME must equal Name in FyneApp.toml: fyne package names the .app and .exe after it.
readonly APP_NAME="ProRes Raw to DNG Converter"
readonly ARCHIVE_PREFIX="rawconv"
readonly VERSION_VAR="rawconv/internal/version.Version"
readonly MAC_CLI_DIR="cli"
# rawconv's own license (GPL 3.0), shipped beside the binaries as the GPL requires.
readonly LICENSE_FILE="LICENSE"
readonly ARCHIVE_LICENSE_NAME="LICENSE.txt"
# The oldest macOS the app runs on; build-ffmpeg.sh uses the same value.
readonly MACOS_MIN_VERSION="12.0"

main() {
	if [[ $# -ne 3 ]]; then
		echo "usage: $0 <version> <ffmpeg-dir> <dist-dir>" >&2
		exit 2
	fi
	local version="$1"
	local ffmpeg_dir
	ffmpeg_dir="$(cd "$2" && pwd)"
	local dist_dir
	dist_dir="$(mkdir -p "$3" && cd "$3" && pwd)"
	local repo_root
	repo_root="$(cd "$(dirname "$0")/.." && pwd)"
	cd "$repo_root"

	local goos goarch
	goos="$(go env GOOS)"
	goarch="$(go env GOARCH)"
	local name
	name="${ARCHIVE_PREFIX}-${version}-$(platform_name "$goos")-${goarch}"
	local stage="${dist_dir}/${name}"
	rm -rf "$stage"
	mkdir -p "$stage"
	cp "$LICENSE_FILE" "${stage}/${ARCHIVE_LICENSE_NAME}"

	case "$goos" in
	darwin)
		export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-$MACOS_MIN_VERSION}"
		package_gui "$goos" "$version"
		local app="${stage}/${APP_NAME}.app"
		mv "${APP_NAME}.app" "$app"
		cp "$ffmpeg_dir"/* "${app}/Contents/Resources/"
		build_cli "$version" "${stage}/${MAC_CLI_DIR}"
		cp "$ffmpeg_dir"/* "${stage}/${MAC_CLI_DIR}/"
		# ditto keeps the bundle's symlinks and permissions intact; without the
		# --no* flags it adds a "._" file for every extended attribute.
		ditto -c -k --norsrc --noextattr --noqtn --keepParent "$stage" "${stage}.zip"
		;;
	windows)
		package_gui "$goos" "$version"
		mv "${APP_NAME}.exe" "$stage/"
		build_cli "$version" "$stage"
		cp "$ffmpeg_dir"/* "$stage/"
		(cd "$dist_dir" && zip -qr "${name}.zip" "$name")
		;;
	linux)
		go build -trimpath -ldflags "-s -w -X=${VERSION_VAR}=${version}" -o "${stage}/rawconv" .
		build_cli "$version" "$stage"
		cp "$ffmpeg_dir"/* "$stage/"
		tar -C "$dist_dir" -cJf "${stage}.tar.xz" "$name"
		;;
	*)
		echo "unsupported platform: ${goos}" >&2
		exit 1
		;;
	esac
	rm -rf "$stage"
	ls -l "$dist_dir"
}

# platform_name is the OS name users look for in a download list.
platform_name() {
	if [[ "$1" == "darwin" ]]; then
		echo "macos"
	else
		echo "$1"
	fi
}

# package_gui builds the GUI with fyne package, which adds the icon and, on
# macOS, the .app bundle. fyne merges -ldflags from GOFLAGS into its own.
# Bundles accept only a numeric x.y.z version, so a beta suffix stays in the
# binary and the build number tells betas apart.
package_gui() {
	local goos="$1" version="$2"
	GOFLAGS="-ldflags=-X=${VERSION_VAR}=${version}" fyne package \
		--os "$goos" --release \
		--app-version "${version%%-*}" \
		--app-build "${BUILD_NUMBER:-1}"
}

build_cli() {
	local version="$1" out_dir="$2"
	local exe_suffix
	exe_suffix="$(go env GOEXE)"
	mkdir -p "$out_dir"
	local cmd
	for cmd in rawconv-cli prrinfo; do
		go build -trimpath -ldflags "-s -w -X=${VERSION_VAR}=${version}" \
			-o "${out_dir}/${cmd}${exe_suffix}" "./cmd/${cmd}"
	done
}

main "$@"
