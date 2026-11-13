#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT="$SCRIPT_DIR/wuzapi"
RUN_TEST=0
OUTPUT_SET=0
while [[ $# -gt 0 ]]; do
	case "$1" in
		--run-test)
			RUN_TEST=1
			shift
			;;
		-o|--output)
			if [[ $# -lt 2 ]]; then
				echo "Missing output path after $1" >&2
				exit 2
			fi
			OUTPUT="$2"
			OUTPUT_SET=1
			shift 2
			;;
		-*)
			echo "Unknown option: $1" >&2
			exit 2
			;;
		*)
			if [[ "$OUTPUT_SET" -eq 1 ]]; then
				echo "Output path specified more than once" >&2
				exit 2
			fi
			OUTPUT="$1"
			OUTPUT_SET=1
			shift
			;;
	esac
done

if [[ -z "${TERMUX_VERSION:-}" || -z "${PREFIX:-}" ]]; then
	echo "This script must run inside Termux (TERMUX_VERSION and PREFIX are required)." >&2
	exit 1
fi
for tool in go termux-elf-cleaner strip ffmpeg; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "Required tool not found: $tool" >&2
		exit 1
	fi
done

TMP_PARENT="${TMPDIR:-$PREFIX/tmp}"
mkdir -p "$TMP_PARENT"
TMP_DIR="$(mktemp -d "$TMP_PARENT/wuzapi-build.XXXXXX")"
ORIGINAL_BUNDLE="$TMP_DIR/termux-resources.original"
cp -- "$SCRIPT_DIR/termux-resources.tar.zst" "$ORIGINAL_BUNDLE"
restore_bundle() {
	cp -- "$ORIGINAL_BUNDLE" "$SCRIPT_DIR/termux-resources.tar.zst"
	rm -rf -- "$TMP_DIR"
}
trap restore_bundle EXIT

"$SCRIPT_DIR/build_dynamic_bundle.sh" --output "$TMP_DIR/termux-resources.tar.zst" \
	"$(command -v ffmpeg)"
cp -- "$TMP_DIR/termux-resources.tar.zst" "$SCRIPT_DIR/termux-resources.tar.zst"

cd "$SCRIPT_DIR"
go build -o "$OUTPUT" .
termux-elf-cleaner "$OUTPUT" >/dev/null
strip -s -w "$OUTPUT"
echo "Built $OUTPUT with bundled ffmpeg and Go-generated audio waveforms."

if [[ "$RUN_TEST" -eq 1 ]]; then
	SMOKE_TEST="$TMP_DIR/termux_bundle_smoke"
	go build -o "$SMOKE_TEST" ./tests/termux_bundle_smoke
	"$SMOKE_TEST" "$TMP_DIR/termux-resources.tar.zst"
fi
