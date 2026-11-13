#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT="$SCRIPT_DIR/termux-resources.tar.zst"

if [[ "${1:-}" == "--output" ]]; then
	if [[ $# -lt 3 ]]; then
		echo "Usage: $0 [--output ARCHIVE] EXECUTABLE..." >&2
		exit 2
	fi
	OUTPUT="$2"
	shift 2
fi

if [[ -z "${TERMUX_VERSION:-}" || -z "${PREFIX:-}" ]]; then
	echo "This script must run inside Termux (TERMUX_VERSION and PREFIX are required)." >&2
	exit 1
fi
if [[ $# -eq 0 ]]; then
	echo "Usage: $0 [--output ARCHIVE] EXECUTABLE..." >&2
	exit 2
fi

for tool in ldd readelf tar zstd readlink; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "Required tool not found: $tool" >&2
		exit 1
	fi
done

TMP_PARENT="${TMPDIR:-$PREFIX/tmp}"
mkdir -p "$TMP_PARENT"
TMP_DIR="$(mktemp -d "$TMP_PARENT/wuzapi-bundle.XXXXXX")"
cleanup() {
	rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT

BIN_DIR="$TMP_DIR/bin"
LIB_DIR="$BIN_DIR/lib"
mkdir -p "$LIB_DIR" "$(dirname -- "$OUTPUT")"
declare -A copied_libraries=()
declare -A copied_binaries=()

for requested in "$@"; do
	if [[ ! -f "$requested" || ! -x "$requested" ]]; then
		echo "Not an executable file: $requested" >&2
		exit 1
	fi
	binary="$(readlink -f -- "$requested")"
	if ! readelf -d "$binary" 2>/dev/null | grep -q 'Dynamic section'; then
		echo "Expected a dynamically linked executable: $binary" >&2
		exit 1
	fi
	name="$(basename -- "$requested")"
	if [[ -n "${copied_binaries[$name]:-}" && "${copied_binaries[$name]}" != "$binary" ]]; then
		echo "Executable name collision for $name" >&2
		exit 1
	fi
	copied_binaries[$name]="$binary"
	install -m 0755 "$binary" "$BIN_DIR/$name"

	dependencies="$(LC_ALL=C ldd "$binary")"
	if grep -q 'not found' <<<"$dependencies"; then
		echo "Unresolved shared library while inspecting $binary:" >&2
		grep 'not found' <<<"$dependencies" >&2
		exit 1
	fi
	while IFS= read -r dependency; do
		[[ -n "$dependency" ]] || continue
		case "$dependency" in
			"$PREFIX"/*) ;;
			/system/*|/apex/*|/vendor/*) continue ;;
			*) echo "Unexpected non-Termux shared library for $binary: $dependency" >&2; exit 1 ;;
		esac
		library_name="$(basename -- "$dependency")"
		library="$(readlink -f -- "$dependency")"
		if [[ -n "${copied_libraries[$library_name]:-}" ]]; then
			if [[ "${copied_libraries[$library_name]}" != "$library" ]]; then
				echo "Shared library name collision for $library_name" >&2
				exit 1
			fi
			continue
		fi
		copied_libraries[$library_name]="$library"
		install -m 0644 "$library" "$LIB_DIR/$library_name"
	done < <(awk '$2 == "=>" && $3 ~ /^\// {print $3} $1 ~ /^\// {print $1}' <<<"$dependencies")
done

TEMP_ARCHIVE="$TMP_DIR/termux-resources.tar.zst"
tar -cf - -C "$TMP_DIR" bin | zstd -10 -c > "$TEMP_ARCHIVE"
mv -f -- "$TEMP_ARCHIVE" "$OUTPUT"
echo "Wrote $(du -h "$OUTPUT" | awk '{print $1}') bundle: $OUTPUT"
